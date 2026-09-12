package router

import (
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/go-routeros/routeros"
	"gorm.io/gorm"
)

// ---- Background telemetry poller --------------------------------------
//
// RouterOS's API only ever returns a live snapshot (current CPU load,
// current cumulative interface byte counters) - there is no built-in
// history endpoint. To draw a real time-series chart, something has to
// sample repeatedly and persist those samples. This file is that
// something: a ticker-driven goroutine that polls every Connected
// router on an interval and writes one RouterStat row per sample.
//
// Started once from NewService (see router_service_patch.go), guarded by
// sync.Once so multiple RouterService instances in the same process
// don't each start their own duplicate poller.

const PollInterval = 30 * time.Second

// HotspotTrafficInterface is the RouterOS interface whose rx/tx byte
// counters represent client traffic - matches the bridge buildVPNScript
// creates (`/interface bridge add name=hotspot-bridge`), so this counts
// all LAN/hotspot client traffic, not the WAN or WireGuard tunnel itself.
const HotspotTrafficInterface = "hotspot-bridge"

var (
	pollerOnce sync.Once
)

// StartTelemetryPoller launches the background polling loop if it hasn't
// been started yet in this process. Safe to call multiple times - only
// the first call has any effect. Runs until the process exits; there is
// no explicit stop/shutdown hook here since RouterService instances are
// expected to live for the process's lifetime (same assumption
// EnsureWireGuardServer already makes).
func StartTelemetryPoller(db *gorm.DB) {
	pollerOnce.Do(func() {
		go runTelemetryPollerLoop(db)
	})
}

func runTelemetryPollerLoop(db *gorm.DB) {
	ticker := time.NewTicker(PollInterval)
	defer ticker.Stop()

	// Sample once immediately on startup rather than waiting a full
	// interval, so a freshly (re)started backend doesn't leave the
	// dashboard showing stale/empty data for up to PollInterval.
	pollAllConnectedRouters(db)

	pruneTicker := time.NewTicker(1 * time.Hour)
	defer pruneTicker.Stop()

	for {
		select {
		case <-ticker.C:
			pollAllConnectedRouters(db)
		case <-pruneTicker.C:
			if err := PruneOldStats(db); err != nil {
				log.Println("router: telemetry: prune error:", err)
			}
		}
	}
}

func pollAllConnectedRouters(db *gorm.DB) {
	var vpns []VPN
	if err := db.Where("status = ?", Connected).Find(&vpns).Error; err != nil {
		log.Println("router: telemetry: failed to load connected vpns:", err)
		return
	}

	svc := &RouterService{DB: db}

	for _, vpn := range vpns {
		if err := pollAndStoreOne(svc, *vpn.RouterID); err != nil {
			// Best-effort per router: one router's timeout/API error
			// shouldn't stop the rest of the fleet from being sampled.
			log.Printf("router: telemetry: poll failed for router %d: %v", vpn.RouterID, err)
		}
	}
}

func pollAndStoreOne(s *RouterService, routerID uint) error {
	stat := RouterStat{RouterID: routerID}

	resourceReply, err := s.RunRouterCommand(routerID, "/system/resource/print")
	if err != nil {
		return err
	}
	populateResourceStat(&stat, resourceReply)

	// Traffic counters - best-effort, a failure here still lets CPU/memory
	// through rather than discarding the whole sample.
	if trafficReply, err := s.RunRouterCommand(
		routerID, "/interface/print", "?name="+HotspotTrafficInterface,
	); err != nil {
		log.Printf("router: telemetry: failed to read interface counters for router %d: %v", routerID, err)
	} else {
		populateTrafficStat(&stat, trafficReply)
	}

	// Active hotspot users - best-effort.
	if activeReply, err := s.RunRouterCommand(routerID, "/ip/hotspot/active/print"); err != nil {
		log.Printf("router: telemetry: failed to read active users for router %d: %v", routerID, err)
	} else if activeReply != nil {
		stat.ActiveUsers = len(activeReply.Re)
	}

	return s.DB.Create(&stat).Error
}

func populateResourceStat(stat *RouterStat, reply *routeros.Reply) {
	if reply == nil || len(reply.Re) == 0 {
		return
	}
	row := reply.Re[0].Map

	if v, ok := row["cpu-load"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			stat.CPU = f
		}
	}

	totalStr, hasTotal := row["total-memory"]
	freeStr, hasFree := row["free-memory"]
	if hasTotal && hasFree {
		total, errT := strconv.ParseFloat(totalStr, 64)
		free, errF := strconv.ParseFloat(freeStr, 64)
		if errT == nil && errF == nil && total > 0 {
			stat.Memory = ((total - free) / total) * 100
		}
	}
}

func populateTrafficStat(stat *RouterStat, reply *routeros.Reply) {
	if reply == nil || len(reply.Re) == 0 {
		return
	}
	row := reply.Re[0].Map

	if v, ok := row["rx-byte"]; ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			stat.RxBytes = n
		}
	}
	if v, ok := row["tx-byte"]; ok {
		if n, err := strconv.ParseUint(v, 10, 64); err == nil {
			stat.TxBytes = n
		}
	}
}

// PruneOldStats deletes RouterStat rows older than RetentionWindow, so
// the table doesn't grow unbounded - PollInterval of 30s is ~2,880
// rows/router/day without pruning.
func PruneOldStats(db *gorm.DB) error {
	cutoff := time.Now().Add(-RetentionWindow)
	return db.Where("created_at < ?", cutoff).Delete(&RouterStat{}).Error
}
