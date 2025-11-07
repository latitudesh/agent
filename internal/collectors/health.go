package collectors

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

// ServerHealth represents the complete health status of a server
type ServerHealth struct {
	Timestamp     time.Time      `json:"timestamp"`
	ServerID      string         `json:"server_id"`
	OverallStatus HealthStatus   `json:"overall_status"`
	Hardware      HardwareHealth `json:"hardware"`
	Network       NetworkHealth  `json:"network"`
	Disk          DiskHealth     `json:"disk"`
	Memory        MemoryHealth   `json:"memory"`
	CPU           CPUHealth      `json:"cpu"`
	AgentVersion  string         `json:"agent_version"`
}

// HealthStatus represents the health status level
type HealthStatus string

const (
	StatusHealthy   HealthStatus = "healthy"
	StatusDegraded  HealthStatus = "degraded"
	StatusUnhealthy HealthStatus = "unhealthy"
	StatusUnknown   HealthStatus = "unknown"
)

// HardwareHealth represents hardware component health
type HardwareHealth struct {
	Status      HealthStatus        `json:"status"`
	Temperature *TemperatureMetrics `json:"temperature,omitempty"`
	PowerSupply *PowerSupplyStatus  `json:"power_supply,omitempty"`
	IPMI        *IPMIStatus         `json:"ipmi,omitempty"`
	Fans        []FanStatus         `json:"fans,omitempty"`
}

// TemperatureMetrics represents temperature readings
type TemperatureMetrics struct {
	CPU     float64 `json:"cpu_celsius"`
	Ambient float64 `json:"ambient_celsius"`
	Status  string  `json:"status"` // normal, warning, critical
}

// PowerSupplyStatus represents power supply health
type PowerSupplyStatus struct {
	Status    string              `json:"status"`
	Redundant bool                `json:"redundant"`
	Supplies  []PowerSupplyDetail `json:"supplies"`
}

// PowerSupplyDetail represents individual power supply info
type PowerSupplyDetail struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	Watts  int    `json:"watts"`
}

// IPMIStatus represents IPMI connectivity status
type IPMIStatus struct {
	Reachable       bool      `json:"reachable"`
	LastContact     time.Time `json:"last_contact"`
	FirmwareVersion string    `json:"firmware_version,omitempty"`
}

// FanStatus represents fan health
type FanStatus struct {
	ID     int    `json:"id"`
	Speed  int    `json:"speed_rpm"`
	Status string `json:"status"`
}

// NetworkHealth represents network connectivity health
type NetworkHealth struct {
	Status       HealthStatus       `json:"status"`
	Interfaces   []InterfaceStatus  `json:"interfaces"`
	Connectivity ConnectivityStatus `json:"connectivity"`
	PacketLoss   float64            `json:"packet_loss_percent"`
	Latency      int64              `json:"latency_ms"`
}

// InterfaceStatus represents network interface status
type InterfaceStatus struct {
	Name     string `json:"name"`
	Status   string `json:"status"` // up, down
	Speed    int64  `json:"speed_mbps"`
	Duplex   string `json:"duplex"`
	RxErrors uint64 `json:"rx_errors"`
	TxErrors uint64 `json:"tx_errors"`
	RxBytes  uint64 `json:"rx_bytes"`
	TxBytes  uint64 `json:"tx_bytes"`
}

// ConnectivityStatus represents network connectivity tests
type ConnectivityStatus struct {
	Internet    bool      `json:"internet"`
	Gateway     bool      `json:"gateway"`
	DNS         bool      `json:"dns"`
	LatitudeAPI bool      `json:"latitude_api"`
	LastCheck   time.Time `json:"last_check"`
}

// DiskHealth represents disk and storage health
type DiskHealth struct {
	Status     HealthStatus  `json:"status"`
	Disks      []DiskStatus  `json:"disks"`
	TotalSpace uint64        `json:"total_space_bytes"`
	UsedSpace  uint64        `json:"used_space_bytes"`
	SMART      []SMARTStatus `json:"smart,omitempty"`
}

// DiskStatus represents individual disk status
type DiskStatus struct {
	Device      string  `json:"device"`
	Status      string  `json:"status"`
	Size        uint64  `json:"size_bytes"`
	Temperature float64 `json:"temperature_celsius,omitempty"`
	SMART       bool    `json:"smart_enabled"`
	Type        string  `json:"type"` // disk, partition
}

// SMARTStatus represents SMART disk health data
type SMARTStatus struct {
	Device              string `json:"device"`
	Passed              bool   `json:"passed"`
	ReallocatedSectors  int    `json:"reallocated_sectors"`
	PendingSectors      int    `json:"pending_sectors"`
	UncorrectableErrors int    `json:"uncorrectable_errors"`
	Temperature         int    `json:"temperature_celsius"`
	PowerOnHours        int    `json:"power_on_hours"`
}

// MemoryHealth represents memory health
type MemoryHealth struct {
	Status       HealthStatus `json:"status"`
	Total        uint64       `json:"total_bytes"`
	Used         uint64       `json:"used_bytes"`
	Available    uint64       `json:"available_bytes"`
	UsagePercent float64      `json:"usage_percent"`
	Errors       int          `json:"errors"`
	SwapTotal    uint64       `json:"swap_total_bytes"`
	SwapUsed     uint64       `json:"swap_used_bytes"`
}

// CPUHealth represents CPU health
type CPUHealth struct {
	Status       HealthStatus `json:"status"`
	UsagePercent float64      `json:"usage_percent"`
	Temperature  float64      `json:"temperature_celsius,omitempty"`
	LoadAverage  LoadAverage  `json:"load_average"`
	CoreCount    int          `json:"core_count"`
}

// LoadAverage represents system load averages
type LoadAverage struct {
	Load1  float64 `json:"load_1min"`
	Load5  float64 `json:"load_5min"`
	Load15 float64 `json:"load_15min"`
}

// HealthCollector collects health metrics from the server
type HealthCollector struct {
	logger       *logrus.Logger
	serverID     string
	ipmiTool     string
	smartctlTool string
	agentVersion string
}

// NewHealthCollector creates a new health collector
func NewHealthCollector(serverID string, agentVersion string, logger *logrus.Logger) *HealthCollector {
	return &HealthCollector{
		logger:       logger,
		serverID:     serverID,
		ipmiTool:     "/usr/bin/ipmitool",
		smartctlTool: "/usr/sbin/smartctl",
		agentVersion: agentVersion,
	}
}

// Collect gathers all health metrics
func (hc *HealthCollector) Collect(ctx context.Context) (*ServerHealth, error) {
	hc.logger.Info("Starting health metrics collection")
	start := time.Now()

	health := &ServerHealth{
		Timestamp:     time.Now(),
		ServerID:      hc.serverID,
		AgentVersion:  hc.agentVersion,
		OverallStatus: StatusUnknown,
	}

	// Collect metrics (errors are logged but don't stop collection)
	hc.collectHardwareHealth(ctx, &health.Hardware)
	hc.collectNetworkHealth(ctx, &health.Network)
	hc.collectDiskHealth(ctx, &health.Disk)
	hc.collectMemoryHealth(ctx, &health.Memory)
	hc.collectCPUHealth(ctx, &health.CPU)

	// Calculate overall status
	health.OverallStatus = hc.calculateOverallStatus(health)

	duration := time.Since(start)
	hc.logger.Infof("Health metrics collection completed in %s (status: %s)", duration, health.OverallStatus)

	return health, nil
}

func (hc *HealthCollector) collectHardwareHealth(ctx context.Context, hw *HardwareHealth) {
	// Check IPMI availability
	ipmiStatus := &IPMIStatus{
		Reachable:   hc.checkIPMI(ctx),
		LastContact: time.Now(),
	}
	hw.IPMI = ipmiStatus

	if ipmiStatus.Reachable {
		hc.logger.Debug("IPMI is reachable, collecting sensor data")
		hw.Temperature = hc.collectIPMITemperature(ctx)
		hw.Fans = hc.collectIPMIFans(ctx)
	} else {
		hc.logger.Debug("IPMI not available, skipping hardware sensors")
	}

	hw.Status = hc.evaluateHardwareStatus(hw)
}

func (hc *HealthCollector) checkIPMI(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, hc.ipmiTool, "sensor", "list")
	err := cmd.Run()
	return err == nil
}

func (hc *HealthCollector) collectIPMITemperature(ctx context.Context) *TemperatureMetrics {
	cmd := exec.CommandContext(ctx, hc.ipmiTool, "sensor", "reading", "Temp")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	// Parse temperature from IPMI output (simplified)
	lines := strings.Split(string(output), "\n")
	temp := &TemperatureMetrics{Status: "normal"}

	for _, line := range lines {
		if strings.Contains(line, "CPU") {
			// Parse CPU temp (example: "CPU Temp | 45.000 | degrees C")
			fields := strings.Split(line, "|")
			if len(fields) >= 2 {
				tempStr := strings.TrimSpace(fields[1])
				if t, err := strconv.ParseFloat(tempStr, 64); err == nil {
					temp.CPU = t
				}
			}
		}
	}

	// Determine status based on temperature
	if temp.CPU > 85 {
		temp.Status = "critical"
	} else if temp.CPU > 75 {
		temp.Status = "warning"
	}

	return temp
}

func (hc *HealthCollector) collectIPMIFans(ctx context.Context) []FanStatus {
	cmd := exec.CommandContext(ctx, hc.ipmiTool, "sensor", "reading", "Fan")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	fans := []FanStatus{}
	lines := strings.Split(string(output), "\n")

	for i, line := range lines {
		if strings.Contains(line, "RPM") {
			fan := FanStatus{
				ID:     i + 1,
				Status: "ok",
			}
			// Parse fan speed (simplified)
			fields := strings.Split(line, "|")
			if len(fields) >= 2 {
				speedStr := strings.TrimSpace(fields[1])
				if speed, err := strconv.Atoi(speedStr); err == nil {
					fan.Speed = speed
				}
			}
			fans = append(fans, fan)
		}
	}

	return fans
}

func (hc *HealthCollector) collectNetworkHealth(ctx context.Context, nw *NetworkHealth) {
	// Get interface status
	interfaces := hc.getNetworkInterfaces(ctx)
	nw.Interfaces = interfaces

	// Test connectivity
	connectivity := hc.testConnectivity(ctx)
	nw.Connectivity = connectivity

	// Measure latency to Latitude API
	latency := hc.measureAPILatency(ctx)
	nw.Latency = latency

	nw.Status = hc.evaluateNetworkStatus(nw)
}

func (hc *HealthCollector) getNetworkInterfaces(ctx context.Context) []InterfaceStatus {
	cmd := exec.CommandContext(ctx, "ip", "-s", "-j", "link", "show")
	_, err := cmd.Output()
	if err != nil {
		hc.logger.WithError(err).Warn("Failed to get network interfaces")
		return hc.getNetworkInterfacesFallback(ctx)
	}

	// TODO: Parse JSON output (would need proper JSON parsing implementation)
	// For now, fallback to simple parsing
	return hc.getNetworkInterfacesFallback(ctx)
}

func (hc *HealthCollector) getNetworkInterfacesFallback(ctx context.Context) []InterfaceStatus {
	cmd := exec.CommandContext(ctx, "ip", "link", "show")
	output, err := cmd.Output()
	if err != nil {
		return []InterfaceStatus{}
	}

	interfaces := []InterfaceStatus{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		if strings.Contains(line, ": ") && !strings.HasPrefix(line, " ") {
			// Parse interface name and status
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				name := strings.TrimSuffix(parts[1], ":")
				status := "down"
				if strings.Contains(line, "UP") {
					status = "up"
				}

				interfaces = append(interfaces, InterfaceStatus{
					Name:   name,
					Status: status,
				})
			}
		}
	}

	return interfaces
}

func (hc *HealthCollector) testConnectivity(ctx context.Context) ConnectivityStatus {
	connectivity := ConnectivityStatus{
		LastCheck: time.Now(),
	}

	// Test internet connectivity (Google DNS)
	connectivity.Internet = hc.testConnection(ctx, "8.8.8.8")

	// Test DNS resolution
	connectivity.DNS = hc.testDNS(ctx, "google.com")

	// Test gateway
	connectivity.Gateway = hc.testGateway(ctx)

	// Test Latitude API
	connectivity.LatitudeAPI = hc.testConnection(ctx, "api.latitude.sh")

	return connectivity
}

func (hc *HealthCollector) testConnection(ctx context.Context, host string) bool {
	cmd := exec.CommandContext(ctx, "ping", "-c", "1", "-W", "2", host)
	err := cmd.Run()
	return err == nil
}

func (hc *HealthCollector) testDNS(ctx context.Context, domain string) bool {
	cmd := exec.CommandContext(ctx, "nslookup", domain)
	err := cmd.Run()
	return err == nil
}

func (hc *HealthCollector) testGateway(ctx context.Context) bool {
	// Get default gateway
	cmd := exec.CommandContext(ctx, "ip", "route", "show", "default")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	// Parse gateway IP
	fields := strings.Fields(string(output))
	if len(fields) < 3 {
		return false
	}
	gatewayIP := fields[2]

	// Test gateway connectivity
	return hc.testConnection(ctx, gatewayIP)
}

func (hc *HealthCollector) measureAPILatency(ctx context.Context) int64 {
	start := time.Now()
	hc.testConnection(ctx, "api.latitude.sh")
	return time.Since(start).Milliseconds()
}

func (hc *HealthCollector) collectDiskHealth(ctx context.Context, disk *DiskHealth) {
	// List block devices
	disks := hc.listDisks(ctx)
	disk.Disks = disks

	// Get disk space
	total, used := hc.getDiskSpace(ctx)
	disk.TotalSpace = total
	disk.UsedSpace = used

	// Collect SMART data if smartctl is available
	if _, err := os.Stat(hc.smartctlTool); err == nil {
		smartData := hc.collectSMARTData(ctx, disks)
		disk.SMART = smartData
	}

	disk.Status = hc.evaluateDiskStatus(disk)
}

func (hc *HealthCollector) listDisks(ctx context.Context) []DiskStatus {
	cmd := exec.CommandContext(ctx, "lsblk", "-b", "-d", "-n", "-o", "NAME,SIZE,TYPE")
	output, err := cmd.Output()
	if err != nil {
		hc.logger.WithError(err).Warn("Failed to list disks")
		return []DiskStatus{}
	}

	disks := []DiskStatus{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[2] == "disk" {
			size, _ := strconv.ParseUint(fields[1], 10, 64)
			disks = append(disks, DiskStatus{
				Device: fields[0],
				Status: "ok",
				Size:   size,
				Type:   fields[2],
				SMART:  true,
			})
		}
	}

	return disks
}

func (hc *HealthCollector) getDiskSpace(ctx context.Context) (uint64, uint64) {
	cmd := exec.CommandContext(ctx, "df", "-B1", "/")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0
	}

	lines := strings.Split(string(output), "\n")
	if len(lines) < 2 {
		return 0, 0
	}

	fields := strings.Fields(lines[1])
	if len(fields) < 3 {
		return 0, 0
	}

	total, _ := strconv.ParseUint(fields[1], 10, 64)
	used, _ := strconv.ParseUint(fields[2], 10, 64)

	return total, used
}

func (hc *HealthCollector) collectSMARTData(ctx context.Context, disks []DiskStatus) []SMARTStatus {
	smartData := []SMARTStatus{}

	for _, disk := range disks {
		if !disk.SMART {
			continue
		}

		device := "/dev/" + disk.Device
		cmd := exec.CommandContext(ctx, "sudo", hc.smartctlTool, "-H", "-A", device)
		output, err := cmd.Output()
		if err != nil {
			hc.logger.WithError(err).Warnf("Failed to get SMART data for %s", device)
			continue
		}

		smart := SMARTStatus{
			Device: disk.Device,
			Passed: strings.Contains(string(output), "PASSED"),
		}

		// Parse SMART attributes (simplified)
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			fields := strings.Fields(line)
			if len(fields) < 10 {
				continue
			}

			// Look for specific SMART attributes
			switch fields[0] {
			case "5": // Reallocated Sectors
				smart.ReallocatedSectors, _ = strconv.Atoi(fields[9])
			case "197": // Current Pending Sectors
				smart.PendingSectors, _ = strconv.Atoi(fields[9])
			case "198": // Uncorrectable Errors
				smart.UncorrectableErrors, _ = strconv.Atoi(fields[9])
			case "194": // Temperature
				smart.Temperature, _ = strconv.Atoi(fields[9])
			case "9": // Power On Hours
				smart.PowerOnHours, _ = strconv.Atoi(fields[9])
			}
		}

		smartData = append(smartData, smart)
	}

	return smartData
}

func (hc *HealthCollector) collectMemoryHealth(ctx context.Context, mem *MemoryHealth) {
	// Parse /proc/meminfo
	total, used, available := hc.getMemoryInfo()
	mem.Total = total
	mem.Used = used
	mem.Available = available

	if total > 0 {
		mem.UsagePercent = float64(used) / float64(total) * 100
	}

	// Get swap info
	swapTotal, swapUsed := hc.getSwapInfo()
	mem.SwapTotal = swapTotal
	mem.SwapUsed = swapUsed

	// Check for memory errors in dmesg
	mem.Errors = hc.checkMemoryErrors(ctx)

	mem.Status = hc.evaluateMemoryStatus(mem)
}

func (hc *HealthCollector) getMemoryInfo() (uint64, uint64, uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		hc.logger.WithError(err).Warn("Failed to read /proc/meminfo")
		return 0, 0, 0
	}

	var total, available uint64
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		value, _ := strconv.ParseUint(fields[1], 10, 64)
		value *= 1024 // Convert KB to bytes

		switch fields[0] {
		case "MemTotal:":
			total = value
		case "MemAvailable:":
			available = value
		}
	}

	used := total - available
	return total, used, available
}

func (hc *HealthCollector) getSwapInfo() (uint64, uint64) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}

	var swapTotal, swapFree uint64
	lines := strings.Split(string(data), "\n")

	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		value, _ := strconv.ParseUint(fields[1], 10, 64)
		value *= 1024 // Convert KB to bytes

		switch fields[0] {
		case "SwapTotal:":
			swapTotal = value
		case "SwapFree:":
			swapFree = value
		}
	}

	swapUsed := swapTotal - swapFree
	return swapTotal, swapUsed
}

func (hc *HealthCollector) checkMemoryErrors(ctx context.Context) int {
	cmd := exec.CommandContext(ctx, "dmesg")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	errorCount := 0
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "memory error") ||
			strings.Contains(strings.ToLower(line), "edac") {
			errorCount++
		}
	}

	return errorCount
}

func (hc *HealthCollector) collectCPUHealth(ctx context.Context, cpu *CPUHealth) {
	// Get CPU usage
	usage := hc.getCPUUsage(ctx)
	cpu.UsagePercent = usage

	// Get load average
	load := hc.getLoadAverage()
	cpu.LoadAverage = load

	// Get core count
	cpu.CoreCount = hc.getCoreCount()

	// Get CPU temperature (if available from sensors)
	temp := hc.getCPUTemperature(ctx)
	if temp > 0 {
		cpu.Temperature = temp
	}

	cpu.Status = hc.evaluateCPUStatus(cpu)
}

func (hc *HealthCollector) getCPUUsage(ctx context.Context) float64 {
	// Read CPU stats twice with 1 second interval
	stat1 := hc.readCPUStat()

	// Use context-aware sleep to avoid blocking the main thread
	select {
	case <-time.After(1 * time.Second):
		// Continue with the rest of the code
	case <-ctx.Done():
		return 0
	}

	stat2 := hc.readCPUStat()

	if stat1 == nil || stat2 == nil {
		return 0
	}

	total1 := stat1.user + stat1.nice + stat1.system + stat1.idle + stat1.iowait + stat1.irq + stat1.softirq
	total2 := stat2.user + stat2.nice + stat2.system + stat2.idle + stat2.iowait + stat2.irq + stat2.softirq

	totalDiff := total2 - total1
	idleDiff := stat2.idle - stat1.idle

	if totalDiff == 0 {
		return 0
	}

	usage := 100.0 * (float64(totalDiff-idleDiff) / float64(totalDiff))
	return usage
}

type cpuStat struct {
	user    uint64
	nice    uint64
	system  uint64
	idle    uint64
	iowait  uint64
	irq     uint64
	softirq uint64
}

func (hc *HealthCollector) readCPUStat() *cpuStat {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) < 1 {
		return nil
	}

	fields := strings.Fields(lines[0])
	if len(fields) < 8 || fields[0] != "cpu" {
		return nil
	}

	stat := &cpuStat{}
	stat.user, _ = strconv.ParseUint(fields[1], 10, 64)
	stat.nice, _ = strconv.ParseUint(fields[2], 10, 64)
	stat.system, _ = strconv.ParseUint(fields[3], 10, 64)
	stat.idle, _ = strconv.ParseUint(fields[4], 10, 64)
	stat.iowait, _ = strconv.ParseUint(fields[5], 10, 64)
	stat.irq, _ = strconv.ParseUint(fields[6], 10, 64)
	stat.softirq, _ = strconv.ParseUint(fields[7], 10, 64)

	return stat
}

func (hc *HealthCollector) getLoadAverage() LoadAverage {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return LoadAverage{}
	}

	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return LoadAverage{}
	}

	load1, _ := strconv.ParseFloat(fields[0], 64)
	load5, _ := strconv.ParseFloat(fields[1], 64)
	load15, _ := strconv.ParseFloat(fields[2], 64)

	return LoadAverage{
		Load1:  load1,
		Load5:  load5,
		Load15: load15,
	}
}

func (hc *HealthCollector) getCoreCount() int {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 0
	}

	count := 0
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "processor") {
			count++
		}
	}

	return count
}

func (hc *HealthCollector) getCPUTemperature(ctx context.Context) float64 {
	// Try sensors command first
	cmd := exec.CommandContext(ctx, "sensors")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}

	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		if strings.Contains(line, "Core 0") || strings.Contains(line, "CPU") {
			// Parse temperature (example: "Core 0: +45.0°C")
			fields := strings.Fields(line)
			for _, field := range fields {
				if strings.Contains(field, "°C") {
					tempStr := strings.TrimPrefix(field, "+")
					tempStr = strings.TrimSuffix(tempStr, "°C")
					temp, err := strconv.ParseFloat(tempStr, 64)
					if err == nil {
						return temp
					}
				}
			}
		}
	}

	return 0
}

func (hc *HealthCollector) calculateOverallStatus(health *ServerHealth) HealthStatus {
	statuses := []HealthStatus{
		health.Hardware.Status,
		health.Network.Status,
		health.Disk.Status,
		health.Memory.Status,
		health.CPU.Status,
	}

	unhealthyCount := 0
	degradedCount := 0
	unknownCount := 0

	for _, status := range statuses {
		switch status {
		case StatusUnhealthy:
			unhealthyCount++
		case StatusDegraded:
			degradedCount++
		case StatusUnknown:
			unknownCount++
		}
	}

	// If any component is unhealthy, overall is unhealthy
	if unhealthyCount > 0 {
		return StatusUnhealthy
	}

	// If any component is degraded, overall is degraded
	if degradedCount > 0 {
		return StatusDegraded
	}

	// If all are unknown, overall is unknown
	if unknownCount == len(statuses) {
		return StatusUnknown
	}

	return StatusHealthy
}

func (hc *HealthCollector) evaluateHardwareStatus(hw *HardwareHealth) HealthStatus {
	// Check IPMI reachability
	if hw.IPMI != nil && !hw.IPMI.Reachable {
		return StatusDegraded
	}

	// Check temperature
	if hw.Temperature != nil {
		if hw.Temperature.Status == "critical" {
			return StatusUnhealthy
		}
		if hw.Temperature.Status == "warning" {
			return StatusDegraded
		}
	}

	// Check fans
	for _, fan := range hw.Fans {
		if fan.Status != "ok" || fan.Speed < 500 {
			return StatusDegraded
		}
	}

	// If we have IPMI but no data, consider it unknown
	if hw.IPMI != nil && hw.IPMI.Reachable && hw.Temperature == nil {
		return StatusUnknown
	}

	return StatusHealthy
}

func (hc *HealthCollector) evaluateNetworkStatus(nw *NetworkHealth) HealthStatus {
	// Check if primary interfaces are up
	primaryDown := 0
	for _, iface := range nw.Interfaces {
		if iface.Status == "down" && !strings.HasPrefix(iface.Name, "lo") {
			primaryDown++
		}
	}

	// Check connectivity
	if !nw.Connectivity.Internet || !nw.Connectivity.Gateway {
		return StatusUnhealthy
	}

	if !nw.Connectivity.LatitudeAPI {
		return StatusDegraded
	}

	if primaryDown > 0 {
		return StatusDegraded
	}

	return StatusHealthy
}

func (hc *HealthCollector) evaluateDiskStatus(disk *DiskHealth) HealthStatus {
	// Check disk space
	if disk.TotalSpace > 0 {
		usagePercent := float64(disk.UsedSpace) / float64(disk.TotalSpace) * 100

		if usagePercent > 95 {
			return StatusUnhealthy
		}

		if usagePercent > 85 {
			return StatusDegraded
		}
	}

	// Check SMART status
	for _, smart := range disk.SMART {
		if !smart.Passed {
			return StatusUnhealthy
		}

		if smart.ReallocatedSectors > 0 || smart.PendingSectors > 0 {
			return StatusDegraded
		}

		if smart.UncorrectableErrors > 0 {
			return StatusUnhealthy
		}
	}

	return StatusHealthy
}

func (hc *HealthCollector) evaluateMemoryStatus(mem *MemoryHealth) HealthStatus {
	if mem.UsagePercent > 95 {
		return StatusUnhealthy
	}

	if mem.UsagePercent > 85 {
		return StatusDegraded
	}

	if mem.Errors > 0 {
		return StatusDegraded
	}

	return StatusHealthy
}

func (hc *HealthCollector) evaluateCPUStatus(cpu *CPUHealth) HealthStatus {
	// Check CPU temperature
	if cpu.Temperature > 85 {
		return StatusUnhealthy
	}

	if cpu.Temperature > 75 {
		return StatusDegraded
	}

	// Check CPU usage
	if cpu.UsagePercent > 95 {
		return StatusDegraded
	}

	// Check load average vs core count
	if cpu.CoreCount > 0 {
		loadPerCore := cpu.LoadAverage.Load5 / float64(cpu.CoreCount)
		if loadPerCore > 2.0 {
			return StatusDegraded
		}
	}

	return StatusHealthy
}
