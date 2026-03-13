package domain

import "fmt"

type APIError struct {
	Status  int
	Code    string
	Message string
	Details []ValidationError
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewValidationError(details []ValidationError) *APIError {
	return &APIError{
		Status:  400,
		Code:    "validation_failed",
		Message: "validation failed",
		Details: details,
	}
}

func NewNotFoundError(kind, id string) *APIError {
	return &APIError{
		Status:  404,
		Code:    "not_found",
		Message: fmt.Sprintf("%s %q was not found", kind, id),
	}
}

func NewConflictError(message string) *APIError {
	return &APIError{
		Status:  409,
		Code:    "conflict",
		Message: message,
	}
}

func NewBadRequestError(message string) *APIError {
	return &APIError{
		Status:  400,
		Code:    "bad_request",
		Message: message,
	}
}
