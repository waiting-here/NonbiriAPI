package forward

import "github.com/waiting-here/NonbiriAPI/internal/transportpolicy"

func (r *validatedRequest) withTransport(rule transportpolicy.Rule) (*validatedRequest, error) {
	copy := r.CloneForAttempt()
	if copy.chat == nil || rule == transportpolicy.Passthrough {
		return copy, nil
	}
	physical, err := copy.chat.WithStream(rule.UpstreamStream(copy.Stream))
	if err != nil {
		copy.Clear()
		return nil, err
	}
	copy.chat.Clear()
	copy.chat, copy.Stream = physical, physical.Stream
	return copy, nil
}

func freezeTransport(request *validatedRequest, plan *executionPlan) error {
	if request.chat == nil || plan.transportRule == transportpolicy.Passthrough {
		return nil
	}
	for i := range plan.candidates {
		candidate := &plan.candidates[i]
		source := request
		if candidate.prepared != nil {
			source = candidate.prepared.request
		}
		physical, err := source.withTransport(plan.transportRule)
		if err != nil {
			return err
		}
		if candidate.prepared == nil {
			candidate.prepared = &preparedAttempt{}
		} else {
			candidate.prepared.request.Clear()
		}
		candidate.prepared.request = physical
	}
	return nil
}
