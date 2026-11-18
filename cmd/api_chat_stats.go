package cmd

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"
)

// Per-request and daily records of which engine answered invited chats.
//
// Every invited chat logs one line:
//
//	[chat-engine] engine=managed|direct mode=on|trial|off fallback=<code>|- failed=<code>|- detail="…"
//
// and the process logs one summary per UTC day (at the first request of the
// next day, or within the hour after midnight):
//
//	[chat-daily] day=2026-11-19 managed=41 direct=3 fallbacks=2 fallback_reasons=breaker:1,no-slot:1 breaker_trips=1 managed_failed=1 managed_cost_usd=0.83 requested_direct=0 public=17
//
// The summary covers this process since its start or the last summary, so a
// restart starts it over; the per-request lines are the durable record.

const chatFallbackHeader = "X-Birdy-Chat-Fallback"

type chatDailyStats struct {
	mu              sync.Mutex
	day             string
	managed, direct int
	requestedDirect int
	public          int
	fallbacks       map[string]int
	breakerTrips    int
	managedFailed   int
	managedCost     float64
	now             func() time.Time
	loop            sync.Once
}

var chatDaily = &chatDailyStats{fallbacks: map[string]int{}, now: time.Now}

// roll logs and resets the summary when the UTC day changed. Callers hold mu.
func (s *chatDailyStats) roll() {
	today := s.now().UTC().Format("2006-01-02")
	if s.day == "" {
		s.day = today
		return
	}
	if s.day == today {
		return
	}
	log.Print(s.lineLocked())
	// Field by field: s.mu is held.
	s.day, s.fallbacks = today, map[string]int{}
	s.managed, s.direct, s.requestedDirect, s.public = 0, 0, 0, 0
	s.breakerTrips, s.managedFailed, s.managedCost = 0, 0, 0
}

func (s *chatDailyStats) lineLocked() string {
	reasons := make([]string, 0, len(s.fallbacks))
	total := 0
	for code, n := range s.fallbacks {
		reasons = append(reasons, fmt.Sprintf("%s:%d", code, n))
		total += n
	}
	sort.Strings(reasons)
	r := strings.Join(reasons, ",")
	if r == "" {
		r = "-"
	}
	return fmt.Sprintf("[chat-daily] day=%s managed=%d direct=%d fallbacks=%d fallback_reasons=%s breaker_trips=%d managed_failed=%d managed_cost_usd=%.4f requested_direct=%d public=%d",
		s.day, s.managed, s.direct, total, r, s.breakerTrips, s.managedFailed, s.managedCost, s.requestedDirect, s.public)
}

func (s *chatDailyStats) update(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roll()
	f()
}

// start runs the hourly check that logs a day's summary without waiting for
// the next request. It runs once per process.
func (s *chatDailyStats) start() {
	s.loop.Do(func() {
		go func() {
			for range time.Tick(time.Hour) {
				s.update(func() {})
			}
		}()
	})
}

// logChatEngine records which engine answered an invited chat. code is the
// fallback reason on the direct path, or why a managed answer failed.
func logChatEngine(engine string, route chatRoute, code, detail string) {
	fallback, failed := "-", "-"
	if code != "" && engine == "direct" {
		fallback = code
	} else if code != "" {
		failed = code
	}
	mode := chatManagedMode()
	if mode == "" {
		mode = "off"
	}
	log.Printf("[chat-engine] engine=%s mode=%s fallback=%s failed=%s detail=%q", engine, mode, fallback, failed, detail)
	chatDaily.update(func() {
		switch {
		case engine == "managed":
			chatDaily.managed++
			if code != "" {
				chatDaily.managedFailed++
			}
		case code == "requested-direct":
			chatDaily.direct++
			chatDaily.requestedDirect++
		default:
			chatDaily.direct++
			if code != "" {
				chatDaily.fallbacks[code]++
			}
		}
	})
}

func chatDailyPublic()          { chatDaily.update(func() { chatDaily.public++ }) }
func chatDailyBreakerTrip()     { chatDaily.update(func() { chatDaily.breakerTrips++ }) }
func chatDailyCost(usd float64) { chatDaily.update(func() { chatDaily.managedCost += usd }) }
