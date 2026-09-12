package identitystate

import "errors"

var ErrInvalidTransition = errors.New("invalid verification transition")

// State keeps the customer outcome separate from the current review step.
type State struct {
	Status    string `json:"status"`
	Substatus string `json:"substatus"`
}

func FromSession(status string) (State, error) {
	switch status {
	case "draft":
		return State{"unverified", "documents_required"}, nil
	case "submitted":
		return State{"pending", "manual_review"}, nil
	case "approved":
		return State{"verified", "approved"}, nil
	case "needs_information":
		return State{"pending", "information_required"}, nil
	case "rejected":
		return State{"rejected", "rejected"}, nil
	case "withdrawn", "discarded":
		return State{"unverified", status}, nil
	default:
		return State{}, ErrInvalidTransition
	}
}

func Transition(from, to string) error {
	switch from {
	case "draft":
		if to == "submitted" || to == "discarded" || to == "withdrawn" {
			return nil
		}
	case "submitted":
		if to == "approved" || to == "needs_information" || to == "rejected" || to == "withdrawn" {
			return nil
		}
	case "approved", "needs_information", "rejected":
		if to == "withdrawn" {
			return nil
		}
	}
	return ErrInvalidTransition
}
