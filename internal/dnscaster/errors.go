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
	Message    string
	Errors     []string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API error during %s to %s (status %d): %s %s", e.Operation, e.URL, e.StatusCode, e.Message, e.Errors)
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
func NewAPIError(operation, url string, statusCode int, message string, errors []string) error {
	return &APIError{
		Operation:  operation,
		URL:        url,
		StatusCode: statusCode,
		Message:    message,
		Errors:     errors,
	}
}
