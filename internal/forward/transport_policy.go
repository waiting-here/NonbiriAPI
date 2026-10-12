package forward

import "github.com/waiting-here/NonbiriAPI/internal/transportpolicy"

func (r *validatedRequest) withTransport(rule transportpolicy.Rule) (*validatedRequest, error) {
	if r.chat == nil || rule == transportpolicy.Passthrough {
		return r.CloneForAttempt(), nil
	}
	physical, err := r.chat.WithStream(rule.UpstreamStream(r.Stream))
	if err != nil {
		return nil, err
	}
	copy := chatRequest(physical)
	r.copyMetadata(copy)
	return copy, nil
}

func freezeTransport(request *validatedRequest, plan *executionPlan) error {
	if request.chat == nil || plan.transportRule == transportpolicy.Passthrough {
		return nil
	}
	prepared := make(map[*preparedAttempt]*preparedAttempt)
	for i := range plan.candidates {
		candidate := &plan.candidates[i]
		if shared, ok := prepared[candidate.prepared]; ok {
			candidate.prepared = shared
			continue
		}
		source := candidate.prepared.source(request)
		physical, err := source.withTransport(plan.transportRule)
		if err != nil {
			return err
		}
		previous := candidate.prepared
		if previous == nil {
			candidate.prepared = &preparedAttempt{}
		} else {
			candidate.prepared.request.Clear()
		}
		candidate.prepared.request = physical
		prepared[previous] = candidate.prepared
	}
	return nil
}
