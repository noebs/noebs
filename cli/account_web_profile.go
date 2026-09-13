package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/adonese/noebs/internal/tenantauth"
	"github.com/adonese/noebs/internal/workloadauth"
)

type accountWebProfiles interface {
	Resolve(context.Context, tenantauth.Principal, string, string) (int64, error)
	Create(context.Context, tenantauth.Principal, string, string, string) error
}

type accountWebProfileClient struct {
	*identityProfileProjectionResolver
}

func (r *accountWebProfileClient) Create(ctx context.Context, principal tenantauth.Principal, fullname, requestID, sourceIP string) error {
	body, err := json.Marshal(struct {
		Fullname string `json:"fullname"`
	}{Fullname: fullname})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+"/consumer/auth/profile", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(workloadauth.HeaderRequestID, requestID)
	if err := setGatewayPrincipalHeaders(req.Header, principal, "", 0, sourceIP); err != nil {
		return err
	}
	if err := r.signers.Sign(string(serviceRoleIdentityAuth), req, body); err != nil {
		return err
	}
	client := *r.client
	client.CheckRedirect = workloadauth.RejectRedirect
	response, err := client.Do(req)
	if err != nil {
		return errProfileProjectionUnavailable
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode == http.StatusCreated {
		return nil
	}
	// An interrupted successful creation can be resumed without creating another account.
	if response.StatusCode == http.StatusConflict {
		_, err := r.Resolve(ctx, principal, requestID, sourceIP)
		return err
	}
	return errors.New("account profile could not be completed")
}
