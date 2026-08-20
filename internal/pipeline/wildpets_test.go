package pipeline

import (
	"slices"
	"testing"
	"time"

	"github.com/whoisnian/rocom-capture/internal/gamedata"
	"github.com/whoisnian/rocom-capture/internal/scene"
)

func TestWildKindsWeightAndVoiceBoundaries(t *testing.T) {
	info := gamedata.PetBaseInfo{WeightLow: 100, WeightHigh: 200}
	tests := []struct {
		name   string
		actor  scene.NpcActor
		want   []string
		absent []string
	}{
		{name: "big medal edge", actor: scene.NpcActor{Weight: 198}, want: []string{"weight-big"}, absent: []string{"weight-big-max"}},
		{name: "big max", actor: scene.NpcActor{Weight: 200}, want: []string{"weight-big", "weight-big-max"}},
		{name: "small medal edge", actor: scene.NpcActor{Weight: 102}, want: []string{"weight-small"}, absent: []string{"weight-small-max"}},
		{name: "small max", actor: scene.NpcActor{Weight: 100}, want: []string{"weight-small", "weight-small-max"}},
		{name: "high voice edge", actor: scene.NpcActor{Weight: 150, Voice: 96}, want: []string{"voice-high"}, absent: []string{"voice-high-max"}},
		{name: "high voice max", actor: scene.NpcActor{Weight: 150, Voice: 100}, want: []string{"voice-high", "voice-high-max"}},
		{name: "low voice edge", actor: scene.NpcActor{Weight: 150, Voice: -96}, want: []string{"voice-low"}, absent: []string{"voice-low-max"}},
		{name: "low voice max", actor: scene.NpcActor{Weight: 150, Voice: -100}, want: []string{"voice-low", "voice-low-max"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wildKinds(tt.actor, &info)
			for _, kind := range tt.want {
				if !slices.Contains(got, kind) {
					t.Errorf("wildKinds() = %v, missing %q", got, kind)
				}
			}
			for _, kind := range tt.absent {
				if slices.Contains(got, kind) {
					t.Errorf("wildKinds() = %v, unexpectedly contains %q", got, kind)
				}
			}
		})
	}
}

func TestWildKindsMutationWithoutPetBase(t *testing.T) {
	got := wildKinds(scene.NpcActor{
		Mutation: scene.MutationShiny | scene.MutationChaosTwo,
		GlassType: gamedata.GlassCommon,
	}, nil)
	for _, kind := range []string{"colorful", "shiny", "pollution"} {
		if !slices.Contains(got, kind) {
			t.Errorf("wildKinds() = %v, missing %q", got, kind)
		}
	}
}

func TestWildKindsWeightDoesNotUseRoundedPercentile(t *testing.T) {
	info := gamedata.PetBaseInfo{WeightLow: 0, WeightHigh: 100000}
	got := wildKinds(scene.NpcActor{Weight: 97999}, &info) // 97.999%,展示值会舍入为 98.00%
	if slices.Contains(got, "weight-big") {
		t.Fatalf("wildKinds() = %v, raw 97.999%% must not match weight-big", got)
	}
}

func TestSwitchWildTrackerRetainsTargetsByScene(t *testing.T) {
	seen := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	left := seen.Add(10 * time.Minute)
	pet := &wildPet{actorID: 1, seenAt: seen}
	first := newWildTracker(101)
	first.pets[pet.actorID] = pet
	cs := &connState{wilds: first}

	switchWildTracker(cs, 202, left)
	if cs.wilds.res != 202 || len(cs.wilds.pets) != 0 {
		t.Fatalf("destination tracker = %#v, want empty scene 202", cs.wilds)
	}
	if !pet.left || !pet.seenAt.Equal(left) {
		t.Fatalf("departed pet left=%v seenAt=%v, want stale at %v", pet.left, pet.seenAt, left)
	}

	switchWildTracker(cs, 101, left.Add(time.Minute))
	if cs.wilds != first || cs.wilds.pets[pet.actorID] != pet {
		t.Fatal("returning to scene did not restore its tracker")
	}
}

func TestSwitchWildTrackerSameSceneTeleportMarksActiveStale(t *testing.T) {
	seen := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	teleported := seen.Add(time.Minute)
	ts := newWildTracker(101)
	ts.pets[1] = &wildPet{actorID: 1, seenAt: seen}
	cs := &connState{wilds: ts}

	switchWildTracker(cs, 101, teleported)
	pet := cs.wilds.pets[1]
	if cs.wilds != ts || pet == nil || !pet.left || !pet.seenAt.Equal(teleported) {
		t.Fatalf("same-scene tracker/pet not retained as stale: tracker=%p pet=%#v", cs.wilds, pet)
	}

	// 传送通知后还可能紧跟同场景进入回包,不能让第二次切换延后灰点过期时间。
	switchWildTracker(cs, 101, teleported.Add(time.Second))
	if !pet.seenAt.Equal(teleported) {
		t.Fatalf("already stale target seenAt = %v, want original departure %v", pet.seenAt, teleported)
	}
}

func TestSwitchWildTrackerPrunesExpiredScenes(t *testing.T) {
	now := time.Date(2026, 8, 20, 8, 0, 0, 0, time.UTC)
	old := newWildTracker(101)
	old.pets[1] = &wildPet{actorID: 1, left: true, seenAt: now.Add(-wildStaleTTL - time.Second)}
	recent := newWildTracker(202)
	recent.pets[2] = &wildPet{actorID: 2, left: true, seenAt: now.Add(-wildStaleTTL)}
	cs := &connState{
		wilds:     recent,
		wildByRes: map[int32]*wildTracker{101: old, 202: recent},
	}

	switchWildTracker(cs, 303, now)
	if len(old.pets) != 0 {
		t.Fatalf("expired stale targets were not pruned: %#v", old.pets)
	}
	if recent.pets[2] == nil {
		t.Fatal("target exactly at stale TTL must still be retained")
	}
}
