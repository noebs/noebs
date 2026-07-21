package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/adonese/noebs/parsing"
	"github.com/gofiber/fiber/v2"
)

type userQueryParseResult struct {
	limit     int
	offset    int
	amount    int64
	limitErr  error
	offsetErr error
	amountErr error
}

func TestUserQueryParsersApplyBoundaryValidation(t *testing.T) {
	got := parseUserQueryForTest(t, "/")
	if got.limitErr != nil || got.offsetErr != nil || got.amountErr != nil {
		t.Fatalf("default query errors = limit:%v offset:%v amount:%v; want nil", got.limitErr, got.offsetErr, got.amountErr)
	}
	if got.limit != 100 || got.offset != 0 || got.amount != 0 {
		t.Fatalf("default query = limit:%d offset:%d amount:%d; want 100, 0, 0", got.limit, got.offset, got.amount)
	}

	got = parseUserQueryForTest(t, "/?limit=25&offset=0&amount=0")
	if got.limitErr != nil || got.offsetErr != nil || got.amountErr != nil {
		t.Fatalf("valid query errors = limit:%v offset:%v amount:%v; want nil", got.limitErr, got.offsetErr, got.amountErr)
	}
	if got.limit != 25 || got.offset != 0 || got.amount != 0 {
		t.Fatalf("valid query = limit:%d offset:%d amount:%d; want 25, 0, 0", got.limit, got.offset, got.amount)
	}

	got = parseUserQueryForTest(t, "/?limit=0")
	if !errors.Is(got.limitErr, parsing.ErrInvalidField) {
		t.Fatalf("limit=0 error = %v, want %v", got.limitErr, parsing.ErrInvalidField)
	}
	got = parseUserQueryForTest(t, "/?offset=-1")
	if !errors.Is(got.offsetErr, parsing.ErrInvalidField) {
		t.Fatalf("offset=-1 error = %v, want %v", got.offsetErr, parsing.ErrInvalidField)
	}
	got = parseUserQueryForTest(t, "/?amount=-1")
	if !errors.Is(got.amountErr, parsing.ErrInvalidField) {
		t.Fatalf("amount=-1 error = %v, want %v", got.amountErr, parsing.ErrInvalidField)
	}
	got = parseUserQueryForTest(t, "/?limit=501")
	if !errors.Is(got.limitErr, parsing.ErrInvalidField) {
		t.Fatalf("limit=501 error = %v, want %v", got.limitErr, parsing.ErrInvalidField)
	}
	got = parseUserQueryForTest(t, "/?offset=100001")
	if !errors.Is(got.offsetErr, parsing.ErrInvalidField) {
		t.Fatalf("offset=100001 error = %v, want %v", got.offsetErr, parsing.ErrInvalidField)
	}
}

func parseUserQueryForTest(t *testing.T, target string) userQueryParseResult {
	t.Helper()

	var got userQueryParseResult
	app := fiber.New()
	app.Get("/", func(c *fiber.Ctx) error {
		got.limit, got.limitErr = positiveIntQuery(c, "limit", 100)
		got.offset, got.offsetErr = nonNegativeIntQuery(c, "offset", 0)
		got.amount, got.amountErr = optionalNonNegativeInt64Query(c, "amount")
		return c.SendStatus(fiber.StatusNoContent)
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, target, nil))
	if err != nil {
		t.Fatalf("app.Test(%q) error = %v", target, err)
	}
	defer closeWalletResponseBody(t, resp.Body)
	if resp.StatusCode != fiber.StatusNoContent {
		t.Fatalf("app.Test(%q) status = %d, want %d", target, resp.StatusCode, fiber.StatusNoContent)
	}
	return got
}
