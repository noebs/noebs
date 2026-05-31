package consumer

import (
	"context"
	"errors"
	"testing"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
)

func TestTelecomPaymentPhoneRequiresExplicitPaymentInfoFormat(t *testing.T) {
	got, err := telecomPaymentPhone("MPHONE=249912345678")
	if err != nil {
		t.Fatalf("telecomPaymentPhone() error = %v", err)
	}
	if got != "0249912345678" {
		t.Fatalf("phone = %q, want %q", got, "0249912345678")
	}

	if _, err := telecomPaymentPhone("249912345678"); !errors.Is(err, ErrInvalidPaymentInfo) {
		t.Fatalf("telecomPaymentPhone(invalid) error = %v, want %v", err, ErrInvalidPaymentInfo)
	}
}

func TestMoheArabicPaymentPhoneRequiresExplicitPaymentInfoFormat(t *testing.T) {
	got, err := moheArabicPaymentPhone("ignored/abcdefghij0912345678")
	if err != nil {
		t.Fatalf("moheArabicPaymentPhone() error = %v", err)
	}
	if got != "0912345678" {
		t.Fatalf("phone = %q, want %q", got, "0912345678")
	}

	if _, err := moheArabicPaymentPhone("ignored"); !errors.Is(err, ErrInvalidPaymentInfo) {
		t.Fatalf("moheArabicPaymentPhone(invalid) error = %v, want %v", err, ErrInvalidPaymentInfo)
	}
}

func TestUpdatePaymentInfoBuildsCustomsPaymentInfoFromRequestFields(t *testing.T) {
	var fields ebs_fields.ConsumerBillInquiryFields
	err := updatePaymentInfo(&fields, Bills{
		PayeeID:       "0010030003",
		Bank:          " 016 ",
		DeclarantCode: " DEC-42 ",
	})
	if err != nil {
		t.Fatalf("updatePaymentInfo() error = %v", err)
	}
	if fields.PaymentInfo != "BANKCODE=016/DECLARANTCODE=DEC-42" {
		t.Fatalf("PaymentInfo = %q, want customs bank/declarant fields", fields.PaymentInfo)
	}
}

func TestGetBillsRejectsMissingCustomsPaymentInfoBeforeEBS(t *testing.T) {
	service := &Service{
		Store:      &store.Store{},
		HTTPClient: testHTTPClient(),
	}

	_, _, err := service.GetBills(context.Background(), "tenant-a", Bills{
		PayeeID:       "0010030003",
		DeclarantCode: "DEC-42",
	})
	if !errors.Is(err, ErrInvalidPaymentInfo) {
		t.Fatalf("GetBills(missing bank) error = %v, want %v", err, ErrInvalidPaymentInfo)
	}

	_, _, err = service.GetBills(context.Background(), "tenant-a", Bills{
		PayeeID: "0010030003",
		Bank:    "016",
	})
	if !errors.Is(err, ErrInvalidPaymentInfo) {
		t.Fatalf("GetBills(missing declarant) error = %v, want %v", err, ErrInvalidPaymentInfo)
	}
}

func TestParseDueAmountsRequiresTypedPaymentInfo(t *testing.T) {
	cases := []struct {
		name        string
		payeeID     string
		paymentInfo map[string]any
	}{
		{
			name:    "zain missing total",
			payeeID: "0010010002",
			paymentInfo: map[string]any{
				"unbilledAmount": "10",
				"billedAmount":   "5",
			},
		},
		{
			name:    "zain non-string total",
			payeeID: "0010010002",
			paymentInfo: map[string]any{
				"totalAmount":    15,
				"unbilledAmount": "10",
				"billedAmount":   "5",
			},
		},
		{
			name:    "e15 missing due",
			payeeID: "0010050001",
			paymentInfo: map[string]any{
				"TotalAmount": "15",
			},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDueAmounts(tt.payeeID, tt.paymentInfo)
			if !errors.Is(err, ErrInvalidPaymentInfo) {
				t.Fatalf("parseDueAmounts() error = %v, want %v", err, ErrInvalidPaymentInfo)
			}
		})
	}
}

func TestParseDueAmountsParsesZainPaymentInfo(t *testing.T) {
	got, err := parseDueAmounts("0010010002", map[string]any{
		"totalAmount":    "15",
		"unbilledAmount": "10",
		"billedAmount":   "5",
	})
	if err != nil {
		t.Fatalf("parseDueAmounts() error = %v", err)
	}
	if got.Amount != "15" || got.DueAmount != "10" || got.PaidAmount != "5" {
		t.Fatalf("amounts = %+v", got)
	}
}
