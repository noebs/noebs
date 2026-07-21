package fx

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
)

const maxResponseBytes int64 = 2 << 20

// executeProviderRequest refuses redirects and verifies the URL recorded on the
// response before any provider-controlled bytes are parsed. Source URLs are
// catalog data, but they are still restricted to the endpoint pinned by each
// provider implementation.
func executeProviderRequest(client *http.Client, request *http.Request, expectedURL *url.URL) (*http.Response, error) {
	if client == nil || request == nil || expectedURL == nil {
		return nil, ErrMissingProvider
	}
	pinnedClient := *client
	pinnedClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := pinnedClient.Do(request)
	if err != nil {
		return nil, providerError{kind: ErrTemporary, err: err}
	}
	// net/http's default transport records the request on the response. Small
	// test/custom transports are allowed to omit it; redirects are disabled, so
	// the request we issued is the only URL the Client layer could have followed.
	if response != nil && response.Request == nil {
		response.Request = request
	}
	if err := validateProviderResponseURL(response, expectedURL); err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return nil, err
	}
	return response, nil
}

func validateProviderResponseURL(response *http.Response, expected *url.URL) error {
	if response == nil || response.Request == nil || response.Request.URL == nil || expected == nil {
		return providerError{kind: ErrInvalidSourceHost, err: errorsNew("missing final response URL")}
	}
	actual := response.Request.URL
	if actual.Scheme != expected.Scheme ||
		actual.Host != expected.Host ||
		actual.EscapedPath() != expected.EscapedPath() ||
		actual.RawQuery != expected.RawQuery ||
		actual.ForceQuery != expected.ForceQuery ||
		actual.Fragment != "" ||
		actual.User != nil ||
		actual.Opaque != "" {
		return providerError{kind: ErrInvalidSourceHost, err: fmt.Errorf("unexpected final URL %q", actual.Redacted())}
	}
	return nil
}

func readProviderResponse(response *http.Response, allowedMediaTypes ...string) ([]byte, error) {
	if response == nil {
		return nil, providerError{kind: ErrTemporary, err: errorsNew("nil HTTP response")}
	}
	if response.Body == nil {
		return nil, providerError{kind: ErrTemporary, err: errorsNew("nil HTTP response body")}
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusRequestTimeout || response.StatusCode >= 500 {
		return nil, providerError{kind: ErrTemporary, err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, providerError{kind: ErrInvalidResponse, err: fmt.Errorf("HTTP status %d", response.StatusCode)}
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil {
		return nil, providerError{kind: ErrUnexpectedMediaType, err: err}
	}
	allowed := false
	for _, candidate := range allowedMediaTypes {
		if mediaType == candidate {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, providerError{kind: ErrUnexpectedMediaType, err: fmt.Errorf("got %q", mediaType)}
	}
	limited := io.LimitReader(response.Body, maxResponseBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, providerError{kind: ErrTemporary, err: err}
	}
	if int64(len(body)) > maxResponseBytes {
		return nil, ErrResponseTooLarge
	}
	if len(body) == 0 {
		return nil, providerError{kind: ErrInvalidResponse, err: errorsNew("empty body")}
	}
	return body, nil
}

type staticError string

func (e staticError) Error() string { return string(e) }

func errorsNew(message string) error { return staticError(message) }
