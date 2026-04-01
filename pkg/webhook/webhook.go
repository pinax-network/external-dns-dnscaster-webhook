package webhook

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/pkg/errors"
	"sigs.k8s.io/external-dns/endpoint"
	"sigs.k8s.io/external-dns/plan"
	externaldnsprovider "sigs.k8s.io/external-dns/provider"

	"github.com/pinax-network/external-dns-dnscaster-webhook/internal/log"
	"github.com/pinax-network/external-dns-dnscaster-webhook/pkg/metrics"
)

const (
	contentTypeHeader    = "Content-Type"
	contentTypePlaintext = "text/plain"
	acceptHeader         = "Accept"
	varyHeader           = "Vary"
)

// Webhook for external dns provider.
type Webhook struct {
	provider externaldnsprovider.Provider
}

// New creates a new instance of the Webhook.
func New(provider externaldnsprovider.Provider) *Webhook {
	p := Webhook{provider: provider}

	return &p
}

// Records handles the get request for records.
func (p *Webhook) Records(w http.ResponseWriter, r *http.Request) {
	m := metrics.Get()

	err := p.acceptHeaderCheck(w, r)
	if err != nil {
		requestLog(r).With("error", err).Error("accept header check failed")
		m.MarkOperation("records", false)
		return
	}

	ctx := r.Context()
	records, err := p.provider.Records(ctx)
	if err != nil {
		requestLog(r).With("error", err).Error("error getting records")
		m.HTTPDNScasterAPIErrors.WithLabelValues(metrics.ProviderName, r.URL.Path, "records").Inc()
		m.MarkOperation("records", false)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	m.RecordsTotal.WithLabelValues(metrics.ProviderName).Set(float64(len(records)))

	w.Header().Set(contentTypeHeader, string(mediaTypeVersion1))
	w.Header().Set(varyHeader, contentTypeHeader)
	err = json.NewEncoder(w).Encode(records)
	if err != nil {
		requestLog(r).With("error", err).Error("error encoding records")
		m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, r.URL.Path).Inc()
		m.MarkOperation("records", false)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	m.MarkOperation("records", true)
}

// ApplyChanges handles the post request for record changes.
func (p *Webhook) ApplyChanges(w http.ResponseWriter, r *http.Request) {
	m := metrics.Get()

	err := p.contentTypeHeaderCheck(w, r)
	if err != nil {
		requestLog(r).With("error", err).Error("content type header check failed")
		m.MarkOperation("apply_changes", false)
		return
	}

	var changes plan.Changes
	ctx := r.Context()
	err = json.NewDecoder(r.Body).Decode(&changes)
	if err != nil {
		m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, r.URL.Path).Inc()
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusBadRequest)

		errMsg := "error decoding changes: " + err.Error()
		_, writeError := fmt.Fprint(w, errMsg)
		if writeError != nil {
			requestLog(r).With("error", writeError).Error("error writing error message to response writer")
			os.Exit(1)
		}
		requestLog(r).With("error", err).Info(errMsg)
		m.MarkOperation("apply_changes", false)
		return
	}

	m.ChangesTotal.WithLabelValues(metrics.ProviderName, r.URL.Path).Add(float64(totalChanges(changes)))
	m.ChangesByTypeTotal.WithLabelValues(metrics.ProviderName, "create").Add(float64(len(changes.Create)))
	m.ChangesByTypeTotal.WithLabelValues(metrics.ProviderName, "update_old").Add(float64(len(changes.UpdateOld)))
	m.ChangesByTypeTotal.WithLabelValues(metrics.ProviderName, "update_new").Add(float64(len(changes.UpdateNew)))
	m.ChangesByTypeTotal.WithLabelValues(metrics.ProviderName, "delete").Add(float64(len(changes.Delete)))

	requestLog(r).With(
		"create", len(changes.Create),
		"update_old", len(changes.UpdateOld),
		"update_new", len(changes.UpdateNew),
		"delete", len(changes.Delete),
	).Debug("executing plan changes")

	err = p.provider.ApplyChanges(ctx, &changes)
	if err != nil {
		requestLog(r).Error("error when applying changes", "error", err)
		m.HTTPDNScasterAPIErrors.WithLabelValues(metrics.ProviderName, r.URL.Path, "apply_changes").Inc()
		m.MarkOperation("apply_changes", false)
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusInternalServerError)

		return
	}
	m.MarkOperation("apply_changes", true)
	w.WriteHeader(http.StatusNoContent)
}

// AdjustEndpoints handles the post request for adjusting endpoints.
func (p *Webhook) AdjustEndpoints(w http.ResponseWriter, r *http.Request) {
	m := metrics.Get()

	err := p.contentTypeHeaderCheck(w, r)
	if err != nil {
		log.Error("content-type header check failed", "req_method", r.Method, "req_path", r.URL.Path)
		m.MarkOperation("adjust_endpoints", false)
		return
	}
	err = p.acceptHeaderCheck(w, r)
	if err != nil {
		log.Error("accept header check failed", "req_method", r.Method, "req_path", r.URL.Path)
		m.MarkOperation("adjust_endpoints", false)
		return
	}

	var pve []*endpoint.Endpoint
	err = json.NewDecoder(r.Body).Decode(&pve)
	if err != nil {
		m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, r.URL.Path).Inc()
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusBadRequest)

		errMessage := fmt.Sprintf("failed to decode request body: %v", err)
		requestLog(r).With("error", err).Info("failed to decode request body")
		_, writeError := fmt.Fprint(w, errMessage)
		if writeError != nil {
			requestLog(r).With("error", writeError).Error("error writing error message to response writer")
			os.Exit(1)
		}
		m.MarkOperation("adjust_endpoints", false)
		return
	}

	pve, err = p.provider.AdjustEndpoints(pve)
	if err != nil {
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusInternalServerError)
		m.HTTPDNScasterAPIErrors.WithLabelValues(metrics.ProviderName, r.URL.Path, "adjust_endpoints").Inc()
		m.MarkOperation("adjust_endpoints", false)
		return
	}
	out, err := json.Marshal(&pve)
	if err != nil {
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusInternalServerError)
		requestLog(r).With("error", err).Error("failed to marshal endpoints")
		m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, r.URL.Path).Inc()
		m.MarkOperation("adjust_endpoints", false)
		return
	}

	w.Header().Set(contentTypeHeader, string(mediaTypeVersion1))
	w.Header().Set(varyHeader, contentTypeHeader)
	_, writeError := fmt.Fprint(w, string(out))
	if writeError != nil {
		requestLog(r).With("error", writeError).Error("error writing response")
		os.Exit(1)
	}
	m.MarkOperation("adjust_endpoints", true)
}

func (p *Webhook) Negotiate(w http.ResponseWriter, r *http.Request) {
	m := metrics.Get()

	err := p.acceptHeaderCheck(w, r)
	if err != nil {
		requestLog(r).With("error", err).Error("accept header check failed")
		m.MarkOperation("negotiate", false)
		return
	}

	b, err := json.Marshal(p.provider.GetDomainFilter())
	if err != nil {
		requestLog(r).Error("failed to marshal domain filter")
		m.HTTPJSONErrors.WithLabelValues(metrics.ProviderName, r.URL.Path).Inc()
		w.WriteHeader(http.StatusInternalServerError)
		m.MarkOperation("negotiate", false)
		return
	}

	w.Header().Set(contentTypeHeader, string(mediaTypeVersion1))
	_, writeError := w.Write(b)
	if writeError != nil {
		requestLog(r).With("error", writeError).Error("error writing response")
		os.Exit(1)
	}
	m.MarkOperation("negotiate", true)
}

func (p *Webhook) contentTypeHeaderCheck(w http.ResponseWriter, r *http.Request) error {
	return p.headerCheck(true, w, r)
}

func (p *Webhook) acceptHeaderCheck(w http.ResponseWriter, r *http.Request) error {
	return p.headerCheck(false, w, r)
}

func (p *Webhook) headerCheck(isContentType bool, w http.ResponseWriter, r *http.Request) error {
	var header string

	if isContentType {
		header = r.Header.Get(contentTypeHeader)
	} else {
		header = r.Header.Get(acceptHeader)
	}

	if header == "" {
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusNotAcceptable)
		headerType := "accept"
		if isContentType {
			headerType = "content-type"
		}
		metrics.Get().HTTPValidationErrors.WithLabelValues(metrics.ProviderName, r.URL.Path, headerType).Inc()

		var msg string
		if isContentType {
			msg = "client must provide a content type"
		} else {
			msg = "client must provide an accept header"
		}
		err := errors.New(msg)

		_, writeErr := fmt.Fprint(w, err.Error())
		if writeErr != nil {
			requestLog(r).With("error", writeErr).Error("error writing error message to response writer")
			os.Exit(1)
		}

		return err
	}

	// as we support only one media type version, we can ignore the returned value
	_, err := checkAndGetMediaTypeHeaderValue(header)
	if err != nil {
		w.Header().Set(contentTypeHeader, contentTypePlaintext)
		w.WriteHeader(http.StatusUnsupportedMediaType)
		headerType := "accept"
		if isContentType {
			headerType = "content-type"
		}
		metrics.Get().HTTPValidationErrors.WithLabelValues(metrics.ProviderName, r.URL.Path, headerType).Inc()

		msg := "client must provide a valid versioned media type in the "
		if isContentType {
			msg += "content type"
		} else {
			msg += "accept header"
		}

		err := errors.Wrap(err, msg)
		_, writeErr := fmt.Fprint(w, err.Error())
		if writeErr != nil {
			requestLog(r).With("error", writeErr).Error("error writing error message to response writer")
			os.Exit(1)
		}

		return err
	}

	return nil
}

func requestLog(r *http.Request) *slog.Logger {
	return log.With("req_method", r.Method, "req_path", r.URL.Path)
}

func totalChanges(changes plan.Changes) int {
	return len(changes.Create) + len(changes.UpdateOld) + len(changes.UpdateNew) + len(changes.Delete)
}
