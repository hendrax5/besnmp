package systems

import (
	"fmt"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
	"github.com/henrygd/beszel/internal/entities/system"
)

// fetchDataViaSNMP performs an SNMP query against the target router/switch
func (sys *System) fetchDataViaSNMP() (*system.CombinedData, error) {
	record, err := sys.getRecord(sys.manager.hub)
	if err != nil {
		return nil, err
	}

	snmpTarget := record.GetString("snmp_targets")
	if snmpTarget == "" {
		return nil, fmt.Errorf("empty snmp_targets")
	}

	parts := strings.Split(snmpTarget, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid snmp_targets format, expected ip:community[:interfaces]")
	}

	ip := parts[0]
	community := parts[1]

	var filterInterfaces []string
	if len(parts) >= 3 && parts[2] != "" {
		filterInterfaces = strings.Split(parts[2], ",")
	}

	client := &gosnmp.GoSNMP{
		Target:    ip,
		Port:      161, // Default SNMP port
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   time.Duration(2) * time.Second,
		Retries:   1,
	}

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("snmp connect error: %w", err)
	}
	defer client.Conn.Close()

	// Get sysUpTime (1.3.6.1.2.1.1.3.0)
	result, err := client.Get([]string{"1.3.6.1.2.1.1.3.0"})
	if err != nil {
		return nil, fmt.Errorf("snmp get uptime error: %w", err)
	}

	now := time.Now()
	var msElapsed uint64
	if !sys.snmpLastPoll.IsZero() {
		msElapsed = uint64(now.Sub(sys.snmpLastPoll).Milliseconds())
	}
	sys.snmpLastPoll = now

	data := &system.CombinedData{
		Stats: system.Stats{
			NetworkInterfaces: make(map[string][4]uint64),
		},
		Info: system.Info{
			Hostname: "SNMP_" + ip,
			Cores:    1, // dummy value to prevent division by zero in UI
		},
	}

	for _, variable := range result.Variables {
		if variable.Name == ".1.3.6.1.2.1.1.3.0" {
			ticks := gosnmp.ToBigInt(variable.Value).Uint64()
			data.Info.Uptime = ticks / 100
		}
	}

	// Fetch Interface names
	// ifDescr = 1.3.6.1.2.1.2.2.1.2
	names, err := client.BulkWalkAll("1.3.6.1.2.1.2.2.1.2")
	if err != nil {
		return data, nil // allow partial data if walk fails
	}
	
	// Fetch In/Out octets (we try High Capacity first 1.3.6.1.2.1.31.1.1.1.6 and 1.3.6.1.2.1.31.1.1.1.10)
	inOctets, _ := client.BulkWalkAll("1.3.6.1.2.1.31.1.1.1.6")
	outOctets, _ := client.BulkWalkAll("1.3.6.1.2.1.31.1.1.1.10")
	
	// fallback to 32 bit if HC is empty
	if len(inOctets) == 0 {
		inOctets, _ = client.BulkWalkAll("1.3.6.1.2.1.2.2.1.10")
		outOctets, _ = client.BulkWalkAll("1.3.6.1.2.1.2.2.1.16")
	}

	// Map indexes to values
	nameMap := make(map[string]string)
	for _, v := range names {
		// OID e.g. .1.3.6.1.2.1.2.2.1.2.5
		parts := strings.Split(v.Name, ".")
		idx := parts[len(parts)-1]
		if b, ok := v.Value.([]byte); ok {
			nameMap[idx] = string(b)
		} else {
			nameMap[idx] = fmt.Sprintf("if%s", idx)
		}
	}

	var totalSent, totalRecv uint64
	var totalSentPerSec, totalRecvPerSec float64

	inMap := make(map[string]uint64)
	for _, v := range inOctets {
		parts := strings.Split(v.Name, ".")
		idx := parts[len(parts)-1]
		inMap[idx] = gosnmp.ToBigInt(v.Value).Uint64()
	}

	outMap := make(map[string]uint64)
	for _, v := range outOctets {
		parts := strings.Split(v.Name, ".")
		idx := parts[len(parts)-1]
		outMap[idx] = gosnmp.ToBigInt(v.Value).Uint64()
	}

	for idx, name := range nameMap {
		// filter processing
		if len(filterInterfaces) > 0 {
			matched := false
			for _, fi := range filterInterfaces {
				if strings.TrimSpace(fi) == name {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		} else {
			// skip virtuals/loopbacks to avoid clutter by default
			if strings.Contains(strings.ToLower(name), "lo") || strings.Contains(strings.ToLower(name), "virtual") {
				continue
			}
		}

		inBytes := inMap[idx]
		outBytes := outMap[idx]

		if prev, ok := sys.snmpNetDeltas[idx]; ok && msElapsed > 0 {
			inDiff := inBytes - prev[0]
			outDiff := outBytes - prev[1]

			// handle 32/64 bit overflow wraps roughly
			if inBytes < prev[0] {
				inDiff = 0 // simplfied reset
			}
			if outBytes < prev[1] {
				outDiff = 0
			}

			// bytes per second
			inPerSec := uint64(float64(inDiff) / (float64(msElapsed) / 1000.0))
			outPerSec := uint64(float64(outDiff) / (float64(msElapsed) / 1000.0))

			// Stats.NetworkInterfaces format: [upload bytes per sec, download bytes per sec, total upload, total download]
			// Notice: from agent's perspective, download is "in", upload is "out".
			data.Stats.NetworkInterfaces[name] = [4]uint64{outPerSec, inPerSec, outBytes, inBytes}

			totalSent += outBytes
			totalRecv += inBytes
			totalSentPerSec += float64(outPerSec)
			totalRecvPerSec += float64(inPerSec)
		}

		// save delta
		sys.snmpNetDeltas[idx] = [2]uint64{inBytes, outBytes}
	}

	// Global bandwidth properties for the main chart
	data.Stats.Bandwidth[0] = uint64(totalSentPerSec) // In agent, Bandwidth represents bytes per 1s
	data.Stats.Bandwidth[1] = uint64(totalRecvPerSec)
	data.Info.BandwidthBytes = totalSent + totalRecv

	return data, nil
}

type SNMPScanResult struct {
	SysDescr   string   `json:"sysDescr"`
	Interfaces []string `json:"interfaces"`
}

// ScanSNMPInterfaces performs an on-demand scan of a given IP:Community.
func ScanSNMPInterfaces(snmpTarget string) (*SNMPScanResult, error) {
	parts := strings.Split(snmpTarget, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid snmp_targets format, expected ip:community[:interfaces]")
	}

	ip := parts[0]
	community := parts[1]

	client := &gosnmp.GoSNMP{
		Target:    ip,
		Port:      161,
		Community: community,
		Version:   gosnmp.Version2c,
		Timeout:   time.Duration(2) * time.Second,
		Retries:   1,
	}

	if err := client.Connect(); err != nil {
		return nil, fmt.Errorf("snmp connect error: %w", err)
	}
	defer client.Conn.Close()

	result := &SNMPScanResult{
		Interfaces: make([]string, 0),
	}

	// sysDescr = 1.3.6.1.2.1.1.1.0
	res, err := client.Get([]string{"1.3.6.1.2.1.1.1.0"})
	if err == nil && len(res.Variables) > 0 {
		if b, ok := res.Variables[0].Value.([]byte); ok {
			result.SysDescr = string(b)
		}
	}

	// ifDescr = 1.3.6.1.2.1.2.2.1.2
	names, err := client.BulkWalkAll("1.3.6.1.2.1.2.2.1.2")
	if err != nil {
		return result, nil // Return what we have
	}

	for _, v := range names {
		if b, ok := v.Value.([]byte); ok {
			result.Interfaces = append(result.Interfaces, string(b))
		}
	}

	return result, nil
}
