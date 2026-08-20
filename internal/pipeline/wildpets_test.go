package pipeline

import (
	"slices"
	"testing"

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
