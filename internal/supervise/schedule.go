package supervise

import (
	"context"
	"time"

	"github.com/databasifylabs/engines/internal/manifest"
)

type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now()
}

func (realClock) After(d time.Duration) <-chan time.Time {
	return time.After(d)
}

func (s *Supervisor) restartSchedules(schedules []manifest.Schedule) {
	s.stopSchedules()
	ctx, cancel := context.WithCancel(context.Background())
	s.stopSchedules = cancel
	for _, schedule := range schedules {
		go s.tick(ctx, schedule)
	}
}

func (s *Supervisor) tick(ctx context.Context, schedule manifest.Schedule) {
	for {
		now := s.clock.Now()
		select {
		case <-ctx.Done():
			return
		case <-s.clock.After(schedule.Next(now).Sub(now)):
			s.fire(schedule.Name)
		}
	}
}

func (s *Supervisor) fire(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.units[name]
	if !ok || !u.enabled || u.running() {
		return
	}
	s.logs.event(name, "triggered by schedule")
	s.launch(u)
}
