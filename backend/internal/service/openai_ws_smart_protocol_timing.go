package service

import (
	"context"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
)

func recordSmartProtocolDecision(ctx context.Context, d openAISmartProtocolDecision) {
	requesttiming.SetProtocolDecision(ctx, requesttiming.ProtocolDecision{
		Ingress: "ws", Upstream: d.Protocol, Candidate: d.Candidate, Mode: d.Mode,
		Reason: d.Reason, PredictionSource: d.Source, PredictedOutput: d.PredictedOutput, Samples: d.Samples,
	})
	// Fixed enum values and numeric aggregates only. No caller-supplied labels.
	slog.InfoContext(ctx, "openai_smart_protocol_decision", "ingress_protocol", "ws", "upstream_protocol", d.Protocol, "candidate_protocol", d.Candidate, "mode", d.Mode, "reason", d.Reason, "prediction_source", d.Source, "predicted_output", d.PredictedOutput, "samples", d.Samples)
}
