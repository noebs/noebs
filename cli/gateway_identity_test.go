package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	gateway "github.com/adonese/noebs/apigateway"
	"google.golang.org/grpc/metadata"
)

func setTestGatewayUserIdentityHeaders(req *http.Request) {
	setGatewayUserIdentityHeaders(req, 1, "test-tenant", "0912345678")
}

func setGatewayUserIdentityHeaders(req *http.Request, userID int64, tenantID, mobile string) {
	req.Header.Set(gateway.GatewayTenantIDHeader, tenantID)
	req.Header.Set(gateway.GatewayUserIDHeader, strconv.FormatInt(userID, 10))
	if mobile != "" {
		req.Header.Set(gateway.GatewayMobileHeader, mobile)
	}
}

func gatewayUserIdentityContext(userID int64, tenantID, mobile string) context.Context {
	values := []string{
		strings.ToLower(gateway.GatewayTenantIDHeader), tenantID,
		strings.ToLower(gateway.GatewayUserIDHeader), strconv.FormatInt(userID, 10),
	}
	if mobile != "" {
		values = append(values, strings.ToLower(gateway.GatewayMobileHeader), mobile)
	}
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(values...))
}
