package router

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// This file breaks router telemetry into small, single-purpose pieces, each
// wrapping exactly one RouterOS API call via RunRouterCommand. They are kept
// separate (rather than one big "get everything" function) so each can be
// tested, cached, or swapped independently, and so a failure in one metric
// (e.g. hotspot not enabled) doesn't take down the others.
// ---------------------------------------------------------------------------

// SystemResource holds the subset of "/system/resource/print" fields the
// dashboard needs.
type SystemResource struct {
	CPULoadPercent int   // "cpu-load", e.g. "23" (already a percent)
	FreeMemory     int64 // "free-memory", bytes
	TotalMemory    int64 // "total-memory", bytes
	UptimeSeconds  int64 // "uptime", RouterOS duration string, e.g. "3d5h12m3s"
}

// MemoryUsedPercent returns 0-100 memory utilization, or 0 if TotalMemory
// is unknown (avoids a divide-by-zero rather than erroring the whole page).
func (r SystemResource) MemoryUsedPercent() int {
	if r.TotalMemory <= 0 {
		return 0
	}
	used := r.TotalMemory - r.FreeMemory
	return int((used * 100) / r.TotalMemory)
}

// fetchSystemResource runs "/system/resource/print" over the router's
// standardized API connection and parses CPU load, memory, and uptime.
func (s *RouterService) fetchSystemResource(routerID uint) (SystemResource, error) {
	reply, err := s.RunRouterCommand(routerID, "/system/resource/print")
	if err != nil {
		return SystemResource{}, fmt.Errorf("fetchSystemResource: %w", err)
	}
	if len(reply.Re) == 0 {
		return SystemResource{}, fmt.Errorf("fetchSystemResource: empty reply from router %d", routerID)
	}

	fields := reply.Re[0].Map

	cpu, _ := strconv.Atoi(strings.TrimSuffix(fields["cpu-load"], "%"))
	free, _ := strconv.ParseInt(fields["free-memory"], 10, 64)
	total, _ := strconv.ParseInt(fields["total-memory"], 10, 64)
	uptime := parseRouterOSDuration(fields["uptime"])

	return SystemResource{
		CPULoadPercent: cpu,
		FreeMemory:     free,
		TotalMemory:    total,
		UptimeSeconds:  int64(uptime.Seconds()),
	}, nil
}

// InterfaceTraffic holds current throughput for one interface, as reported
// by "/interface/monitor-traffic" (a single-shot sample, not a stream).
type InterfaceTraffic struct {
	DownloadBitsPerSec int64 // "rx-bits-per-second"
	UploadBitsPerSec   int64 // "tx-bits-per-second"
}

// DownloadKbps / UploadKbps convert to whole kbps for display, matching the
// scale the existing dashboard charts use (0-60 range).
func (t InterfaceTraffic) DownloadKbps() int { return int(t.DownloadBitsPerSec / 1000) }
func (t InterfaceTraffic) UploadKbps() int   { return int(t.UploadBitsPerSec / 1000) }

// fetchInterfaceTraffic runs a single-shot "/interface/monitor-traffic" on
// the given interface (the hotspot bridge, by default) and parses the
// current throughput. RouterOS's monitor-traffic normally streams; passing
// "once=" makes it return a single sentence instead of holding the
// connection open, which is what we want for a point-in-time dashboard read.
func (s *RouterService) fetchInterfaceTraffic(routerID uint, ifaceName string) (InterfaceTraffic, error) {
	reply, err := s.RunRouterCommand(
		routerID,
		"/interface/monitor-traffic",
		fmt.Sprintf("=interface=%s", ifaceName),
		"=once=",
	)
	if err != nil {
		return InterfaceTraffic{}, fmt.Errorf("fetchInterfaceTraffic(%s): %w", ifaceName, err)
	}
	if len(reply.Re) == 0 {
		return InterfaceTraffic{}, fmt.Errorf("fetchInterfaceTraffic(%s): empty reply from router %d", ifaceName, routerID)
	}

	fields := reply.Re[0].Map
	rx, _ := strconv.ParseInt(fields["rx-bits-per-second"], 10, 64)
	tx, _ := strconv.ParseInt(fields["tx-bits-per-second"], 10, 64)

	return InterfaceTraffic{DownloadBitsPerSec: rx, UploadBitsPerSec: tx}, nil
}

// fetchHotspotActiveUsers runs "/ip/hotspot/active/print" and returns a
// simple count of currently connected hotspot clients.
func (s *RouterService) fetchHotspotActiveUsers(routerID uint) (int, error) {
	reply, err := s.RunRouterCommand(routerID, "/ip/hotspot/active/print")
	if err != nil {
		return 0, fmt.Errorf("fetchHotspotActiveUsers: %w", err)
	}
	return len(reply.Re), nil
}

// parseRouterOSDuration parses RouterOS's compact duration format
// (e.g. "3d5h12m3s", "12m3s", "45s") into a time.Duration. Unknown/empty
// input returns 0 rather than erroring, since uptime is non-critical.
func parseRouterOSDuration(s string) time.Duration {
	if s == "" {
		return 0
	}

	var total time.Duration
	var num strings.Builder

	for _, r := range s {
		if r >= '0' && r <= '9' {
			num.WriteRune(r)
			continue
		}

		val, err := strconv.Atoi(num.String())
		num.Reset()
		if err != nil {
			continue
		}

		switch r {
		case 'w':
			total += time.Duration(val) * 7 * 24 * time.Hour
		case 'd':
			total += time.Duration(val) * 24 * time.Hour
		case 'h':
			total += time.Duration(val) * time.Hour
		case 'm':
			total += time.Duration(val) * time.Minute
		case 's':
			total += time.Duration(val) * time.Second
		}
	}

	return total
}

// BuildDashboardStats composes the individual RouterOS reads above into one
// DashboardStatPoint. Each sub-fetch is independent: if one fails (e.g. the
// hotspot interface isn't up yet), the others still populate and the failed
// metric is simply left at zero rather than failing the whole dashboard.
func (s *RouterService) BuildDashboardStats(routerID uint) DashboardStatPoint {
	point := DashboardStatPoint{Time: time.Now().Format("15:04")}

	// if res, err := s.fetchSystemResource(routerID); err == nil {
	// 	point.CPU = res.CPULoadPercent
	// 	point.Memory = res.MemoryUsedPercent()
	// }

	// if traffic, err := s.fetchInterfaceTraffic(routerID, "hotspot-bridge"); err == nil {
	// 	point.Download = traffic.DownloadKbps()
	// 	point.Upload = traffic.UploadKbps()
	// }

	// if count, err := s.fetchHotspotActiveUsers(routerID); err == nil {
	// 	point.ActiveUsers = count
	// }
	// _, e := s.RunRouterCommand(2, "/system resource print")
	// if e != nil {
	// 	fmt.Println("failed to run script")
	// } else {
	// 	fmt.Println("success!")
	// }

	return point
}
