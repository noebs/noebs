package workloadauth

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
)

func readRequestBody(req *http.Request) ([]byte, error) {
	if req == nil {
		return nil, ErrInvalidRequest
	}
	if req.Body == nil {
		return []byte{}, nil
	}
	if req.GetBody != nil {
		copyBody, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrBodyRead, err)
		}
		body, readErr := io.ReadAll(copyBody)
		closeErr := copyBody.Close()
		if readErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrBodyRead, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrBodyRead, closeErr)
		}
		return body, nil
	}

	original := req.Body
	body, err := io.ReadAll(original)
	if err != nil {
		req.Body = &readCloser{
			Reader: io.MultiReader(bytes.NewReader(body), original),
			Closer: original,
		}
		return nil, fmt.Errorf("%w: %w", ErrBodyRead, err)
	}
	req.Body = &readCloser{Reader: bytes.NewReader(body), Closer: original}
	return body, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}
