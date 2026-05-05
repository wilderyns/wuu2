package main

import (
	"sync/atomic"
	"testing"
	"time"

	"wuu2/internal/config"
	"wuu2/internal/integrations/applemusic"
	"wuu2/internal/lib/persistence"
	"wuu2/internal/model"
)

func TestRetroAchievementsUpdateIntervalAddsOneMinute(t *testing.T) {
	if got := retroAchievementsUpdateInterval(2 * time.Minute); got != 3*time.Minute {
		t.Fatalf("expected interval of 3m, got %s", got)
	}
}

func TestRunTimedUpdaterSchedulesRetroAchievementsIndependently(t *testing.T) {
	previousOffset := retroAchievementsUpdateOffset
	retroAchievementsUpdateOffset = 50 * time.Millisecond
	defer func() {
		retroAchievementsUpdateOffset = previousOffset
	}()

	previousSteamUpdate := steamUpdateFn
	previousAppleMusicUpdate := appleMusicUpdateFn
	previousRetroAchievementsUpdate := retroAchievementsUpdateFn
	defer func() {
		steamUpdateFn = previousSteamUpdate
		appleMusicUpdateFn = previousAppleMusicUpdate
		retroAchievementsUpdateFn = previousRetroAchievementsUpdate
	}()

	var retroRuns atomic.Int32
	secondRunAt := make(chan time.Duration, 1)
	startedAt := time.Now()

	steamUpdateFn = func(_ config.Config, _ *model.Wuu2) {}
	appleMusicUpdateFn = func(_ *applemusic.Client, _ *model.Wuu2) {}
	retroAchievementsUpdateFn = func(_ config.Config, _ *model.Wuu2) {
		runCount := retroRuns.Add(1)
		if runCount == 2 {
			select {
			case secondRunAt <- time.Since(startedAt):
			default:
			}
		}
	}

	cfg := config.Config{
		RetroAchievementsEnabled: true,
		UpdateIntervalMinutes:    100 * time.Millisecond,
	}

	store := persistence.NewSnapshotStore(persistence.SnapshotFilePathForDirectory(t.TempDir()))
	stop := make(chan struct{})
	done := make(chan struct{})

	go func() {
		runTimedUpdater(cfg, store, nil, nil, stop)
		close(done)
	}()

	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("timed updater did not stop")
		}
	}()

	select {
	case elapsed := <-secondRunAt:
		if elapsed >= 175*time.Millisecond {
			t.Fatalf("expected second RetroAchievements update before the second core tick, got %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second RetroAchievements update")
	}
}
