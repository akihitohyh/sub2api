package requesttiming

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSmartProtocolTimingSnapshotCompatibility(t *testing.T) {
	c := New(time.Now(), 0)
	ctx := With(context.Background(), c)
	SetProtocolDecision(ctx, ProtocolDecision{Ingress: "ws", Upstream: "http", Candidate: "http", Mode: "auto", PredictionSource: "heuristic_cap"})
	SetProtocolDecision(ctx, ProtocolDecision{Upstream: "ws"})
	var first, second Snapshot
	c.WhenFinished(func(s Snapshot) { first = s })
	c.Finish(200, false)
	require.Equal(t, "http", first.ProtocolDecision.Upstream)
	first.ProtocolDecision.Upstream = "mutated"
	c.WhenFinished(func(s Snapshot) { second = s })
	require.Equal(t, "http", second.ProtocolDecision.Upstream)
	var legacy Snapshot
	require.NoError(t, json.Unmarshal([]byte(`{"version":1}`), &legacy))
	require.Nil(t, legacy.ProtocolDecision)
	raw, err := json.Marshal(legacy)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "protocol_decision")
}
