package dnscaster

import "fmt"

// NetworkError represents network-related errors.
type NetworkError struct {
	Operation string
	URL       string
	Err       error
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error during %s to %s: %v", e.Operation, e.URL, e.Err)
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

// DataError represents data marshaling/unmarshaling errors.
type DataError struct {
	Operation string
	DataType  string
	Err       error
}

func (e *DataError) Error() string {
	return fmt.Sprintf("data error during %s of %s: %v", e.Operation, e.DataType, e.Err)
}

func (e *DataError) Unwrap() error {
	return e.Err
}

// APIError represents DNScaster API errors.
type APIError struct {
	Operation  string
	URL        string
	StatusCode int
	RequestID  string
	Message    string
	Errors     []string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("API error during %s to %s (status %d): %s %s", e.Operation, e.URL, e.StatusCode, e.Message, e.Errors)
	if e.RequestID != "" {
		// The API returns X-Request-ID on every response and DNScaster support
		// uses it to trace a specific call.
		msg += fmt.Sprintf(" [request-id: %s]", e.RequestID)
	}
	return msg
}

// NewNetworkError creates a new network error.
func NewNetworkError(operation, url string, err error) error {
	return &NetworkError{
		Operation: operation,
		URL:       url,
		Err:       err,
	}
}

// NewDataError creates a new data error.
func NewDataError(operation, dataType string, err error) error {
	return &DataError{
		Operation: operation,
		DataType:  dataType,
		Err:       err,
	}
}

// NewAPIError creates a new API error.
func NewAPIError(operation, url string, statusCode int, requestID, message string, errors []string) error {
	return &APIError{
		Operation:  operation,
		URL:        url,
		StatusCode: statusCode,
		RequestID:  requestID,
		Message:    message,
		Errors:     errors,
	}
}
