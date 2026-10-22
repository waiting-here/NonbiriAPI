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
	failures := make(map[*preparedAttempt]error)
	for i := range plan.candidates {
		candidate := &plan.candidates[i]
		if candidate.preparationError != nil {
			continue
		}
		if failed, ok := failures[candidate.prepared]; ok {
			candidate.preparationError = failed
			continue
		}
		if shared, ok := prepared[candidate.prepared]; ok {
			candidate.prepared = shared
			continue
		}
		source := candidate.prepared.source(request)
		physical, err := source.withTransport(plan.transportRule)
		if err != nil {
			failures[candidate.prepared] = err
			candidate.preparationError = err
			continue
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
