package requesttiming

import "context"

// ProtocolDecision is optional in existing timing JSON. Old rows remain absent.
// Values originate from server enums and bounded numeric aggregates only.
type ProtocolDecision struct {
	Ingress          string `json:"ingress"`
	Upstream         string `json:"upstream"`
	Candidate        string `json:"candidate"`
	Mode             string `json:"mode"`
	Reason           string `json:"reason"`
	PredictionSource string `json:"prediction_source"`
	PredictedOutput  int    `json:"predicted_output,omitempty"`
	Samples          int    `json:"samples"`
}

func SetProtocolDecision(ctx context.Context, d ProtocolDecision) {
	c := From(ctx)
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.finished && c.data.ProtocolDecision == nil {
		c.data.ProtocolDecision = &d
	}
}
