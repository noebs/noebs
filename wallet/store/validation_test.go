package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestEnsureWalletValidation(t *testing.T) {
	validUserID := int64(42)
	cases := []struct {
		name    string
		params  EnsureWalletParams
		wantErr error
	}{
		{
			name:    "missing-tenant",
			params:  EnsureWalletParams{OwnerType: OwnerTypeUser, OwnerID: "user-1", UserID: validUserID, Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrMissingTenantID,
		},
		{
			name:    "invalid-tenant",
			params:  EnsureWalletParams{TenantID: "default", OwnerType: OwnerTypeUser, OwnerID: "user-1", UserID: validUserID, Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrInvalidTenantID,
		},
		{
			name:    "missing-owner-type",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerID: "user-1", UserID: validUserID, Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrMissingOwnerType,
		},
		{
			name:    "invalid-owner-type",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: "unknown", OwnerID: "user-1", Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrInvalidOwnerType,
		},
		{
			name:    "missing-owner-id",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: OwnerTypeUser, UserID: validUserID, Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrMissingOwnerID,
		},
		{
			name:    "missing-currency",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: OwnerTypeUser, OwnerID: "user-1", UserID: validUserID, KYCTier: KYCTierUnverified},
			wantErr: ErrMissingCurrency,
		},
		{
			name:    "missing-kyc-tier",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: OwnerTypeUser, OwnerID: "user-1", UserID: validUserID, Currency: "USD"},
			wantErr: ErrMissingKYCTier,
		},
		{
			name:    "missing-user-id",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: OwnerTypeUser, OwnerID: "user-1", Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrInvalidUserID,
		},
		{
			name:    "invalid-user-id",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: OwnerTypeUser, OwnerID: "user-1", UserID: -1, Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrInvalidUserID,
		},
		{
			name:    "user-id-on-system",
			params:  EnsureWalletParams{TenantID: "tenant", OwnerType: OwnerTypeSystem, OwnerID: "treasury", UserID: validUserID, Currency: "USD", KYCTier: KYCTierUnverified},
			wantErr: ErrInvalidUserID,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Store{}
			_, err := s.EnsureWallet(t.Context(), tc.params)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestValidateEnsureWalletReplay(t *testing.T) {
	params := EnsureWalletParams{
		TenantID:  "tenant",
		OwnerType: OwnerTypeUser,
		OwnerID:   "user-42",
		UserID:    42,
		Currency:  "USD",
		KYCTier:   KYCTierUnverified,
	}
	existing := Wallet{
		TenantID:  params.TenantID,
		OwnerType: params.OwnerType,
		OwnerID:   params.OwnerID,
		UserID:    sql.NullInt64{Int64: params.UserID, Valid: true},
		Currency:  params.Currency,
		KYCTier:   params.KYCTier,
	}
	if err := ValidateEnsureWalletReplay(&existing, params); err != nil {
		t.Fatalf("ValidateEnsureWalletReplay() error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*EnsureWalletParams)
	}{
		{"tenant", func(p *EnsureWalletParams) { p.TenantID = "other" }},
		{"owner-type", func(p *EnsureWalletParams) { p.OwnerType = OwnerTypeMerchant }},
		{"owner-id", func(p *EnsureWalletParams) { p.OwnerID = "user-99" }},
		{"user-id", func(p *EnsureWalletParams) { p.UserID = 99 }},
		{"currency", func(p *EnsureWalletParams) { p.Currency = "AED" }},
		{"kyc-tier", func(p *EnsureWalletParams) { p.KYCTier = "verified" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay := params
			tc.mutate(&replay)
			assertErrorIs(t, ValidateEnsureWalletReplay(&existing, replay), ErrDuplicateWallet)
		})
	}

	assertErrorIs(t, ValidateEnsureWalletReplay(nil, params), ErrDuplicateWallet)

	systemParams := EnsureWalletParams{
		TenantID:  "tenant",
		OwnerType: OwnerTypeSystem,
		OwnerID:   SystemTreasury,
		Currency:  "USD",
		KYCTier:   KYCTierUnverified,
	}
	systemWallet := Wallet{
		TenantID:  systemParams.TenantID,
		OwnerType: systemParams.OwnerType,
		OwnerID:   systemParams.OwnerID,
		Currency:  systemParams.Currency,
		KYCTier:   systemParams.KYCTier,
	}
	if err := ValidateEnsureWalletReplay(&systemWallet, systemParams); err != nil {
		t.Fatalf("ValidateEnsureWalletReplay(system) error = %v", err)
	}
	systemParams.UserID = 42
	assertErrorIs(t, ValidateEnsureWalletReplay(&systemWallet, systemParams), ErrDuplicateWallet)
}

func TestGetWalletValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetWallet(t.Context(), "", uuid.New())
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetWallet(t.Context(), "default", uuid.New())
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetWallet(t.Context(), "tenant", uuid.Nil)
	assertErrorIs(t, err, ErrMissingWalletID)
}

func TestEnsureSystemWalletsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.EnsureSystemWallets(t.Context(), "", "USD", KYCTierUnverified)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.EnsureSystemWallets(t.Context(), "default", "USD", KYCTierUnverified)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.EnsureSystemWallets(t.Context(), "tenant", "", KYCTierUnverified)
	assertErrorIs(t, err, ErrMissingCurrency)

	_, err = s.EnsureSystemWallets(t.Context(), "tenant", "USD", "")
	assertErrorIs(t, err, ErrMissingKYCTier)
}

func TestListWalletLedgerEntriesValidation(t *testing.T) {
	s := &Store{}
	filter := WalletLedgerEntryFilter{TenantID: "tenant", WalletID: uuid.New(), Limit: 10}

	invalid := filter
	invalid.TenantID = ""
	_, err := s.ListWalletLedgerEntries(t.Context(), invalid)
	assertErrorIs(t, err, ErrMissingTenantID)

	invalid = filter
	invalid.TenantID = "default"
	_, err = s.ListWalletLedgerEntries(t.Context(), invalid)
	assertErrorIs(t, err, ErrInvalidTenantID)

	invalid = filter
	invalid.WalletID = uuid.Nil
	_, err = s.ListWalletLedgerEntries(t.Context(), invalid)
	assertErrorIs(t, err, ErrMissingWalletID)

	invalid = filter
	invalid.EntryType = "inbound"
	_, err = s.ListWalletLedgerEntries(t.Context(), invalid)
	assertErrorIs(t, err, ErrInvalidDirection)

	invalid = filter
	invalid.Limit = 0
	_, err = s.ListWalletLedgerEntries(t.Context(), invalid)
	assertErrorIs(t, err, ErrInvalidLimit)

	invalid = filter
	invalid.Offset = -1
	_, err = s.ListWalletLedgerEntries(t.Context(), invalid)
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestGetWalletByOwnerValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetWalletByOwner(t.Context(), "", OwnerTypeUser, "user-1", "USD")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetWalletByOwner(t.Context(), "default", OwnerTypeUser, "user-1", "USD")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", "", "user-1", "USD")
	assertErrorIs(t, err, ErrMissingOwnerType)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", "unknown", "user-1", "USD")
	assertErrorIs(t, err, ErrInvalidOwnerType)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", OwnerTypeUser, "", "USD")
	assertErrorIs(t, err, ErrMissingOwnerID)

	_, err = s.GetWalletByOwner(t.Context(), "tenant", OwnerTypeUser, "user-1", "")
	assertErrorIs(t, err, ErrMissingCurrency)
}

func TestListWalletsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListWallets(t.Context(), "", 10, 0)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListWallets(t.Context(), "default", 10, 0)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListWallets(t.Context(), "tenant", 0, 0)
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListWallets(t.Context(), "tenant", 10, -1)
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestPostDoubleEntryValidation(t *testing.T) {
	s := &Store{}
	params := DoubleEntryParams{
		TenantID:       "tenant",
		IdempotencyKey: "idempo-1",
		Currency:       "USD",
		ReferenceType:  "p2p",
		DebitWalletID:  uuid.New(),
		CreditWalletID: uuid.New(),
		Amount:         100,
	}

	cases := []struct {
		name    string
		mutate  func(p *DoubleEntryParams)
		wantErr error
	}{
		{"missing-tenant", func(p *DoubleEntryParams) { p.TenantID = "" }, ErrMissingTenantID},
		{"missing-currency", func(p *DoubleEntryParams) { p.Currency = "" }, ErrMissingCurrency},
		{"missing-idempotency", func(p *DoubleEntryParams) { p.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"missing-reference-type", func(p *DoubleEntryParams) { p.ReferenceType = "" }, ErrMissingReferenceType},
		{"missing-wallet", func(p *DoubleEntryParams) { p.DebitWalletID = uuid.Nil }, ErrMissingWalletID},
		{"same-wallet", func(p *DoubleEntryParams) { p.CreditWalletID = p.DebitWalletID }, ErrInvalidWalletPair},
		{"invalid-amount", func(p *DoubleEntryParams) { p.Amount = 0 }, ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			tc.mutate(&p)
			_, err := s.PostDoubleEntry(t.Context(), p)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestValidateDoubleEntryWalletTargets(t *testing.T) {
	debitID := uuid.New()
	creditID := uuid.New()
	params := DoubleEntryParams{
		TenantID:       "tenant",
		IdempotencyKey: "entry-1",
		Currency:       "USD",
		ReferenceType:  "p2p",
		DebitWalletID:  debitID,
		CreditWalletID: creditID,
		Amount:         100,
	}
	debitWallet := &Wallet{
		ID:        debitID,
		TenantID:  "tenant",
		OwnerType: OwnerTypeUser,
		Currency:  "USD",
		Status:    WalletStatusActive,
	}
	creditWallet := &Wallet{
		ID:        creditID,
		TenantID:  "tenant",
		OwnerType: OwnerTypeUser,
		Currency:  "USD",
		Status:    WalletStatusActive,
	}
	if err := validateDoubleEntryWalletTargets(debitWallet, creditWallet, params, doubleEntryMode{}); err != nil {
		t.Fatalf("validateDoubleEntryWalletTargets() error = %v", err)
	}

	foreignDebit := *debitWallet
	foreignDebit.TenantID = "other"
	assertErrorIs(t, validateDoubleEntryWalletTargets(&foreignDebit, creditWallet, params, doubleEntryMode{}), ErrWalletNotFound)

	otherCredit := *creditWallet
	otherCredit.ID = uuid.New()
	assertErrorIs(t, validateDoubleEntryWalletTargets(debitWallet, &otherCredit, params, doubleEntryMode{}), ErrWalletNotFound)

	frozenDebit := *debitWallet
	frozenDebit.Status = WalletStatusFrozen
	assertErrorIs(t, validateDoubleEntryWalletTargets(&frozenDebit, creditWallet, params, doubleEntryMode{}), ErrWalletInactive)

	closedCredit := *creditWallet
	closedCredit.Status = WalletStatusClosed
	assertErrorIs(t, validateDoubleEntryWalletTargets(debitWallet, &closedCredit, params, doubleEntryMode{}), ErrWalletInactive)

	otherCurrency := *creditWallet
	otherCurrency.Currency = "AED"
	assertErrorIs(t, validateDoubleEntryWalletTargets(debitWallet, &otherCurrency, params, doubleEntryMode{}), ErrCurrencyMismatch)

	assertErrorIs(t, validateDoubleEntryWalletTargets(debitWallet, creditWallet, params, doubleEntryMode{AllowSystemDebitOverdraft: true}), ErrSystemDebitWalletRequired)

	systemDebit := *debitWallet
	systemDebit.OwnerType = OwnerTypeSystem
	if err := validateDoubleEntryWalletTargets(&systemDebit, creditWallet, params, doubleEntryMode{AllowSystemDebitOverdraft: true}); err != nil {
		t.Fatalf("validateDoubleEntryWalletTargets(system debit) error = %v", err)
	}
}

func TestHoldValidation(t *testing.T) {
	s := &Store{}
	params := HoldParams{
		TenantID:       "tenant",
		WalletID:       uuid.New(),
		Amount:         100,
		Reason:         "withdrawal",
		ReferenceType:  "withdrawal",
		ReferenceID:    "ref-1",
		IdempotencyKey: "idem-1",
		ExpiresAt:      time.Now().UTC().Add(time.Hour),
	}

	cases := []struct {
		name    string
		mutate  func(p *HoldParams)
		wantErr error
	}{
		{"missing-tenant", func(p *HoldParams) { p.TenantID = "" }, ErrMissingTenantID},
		{"missing-wallet", func(p *HoldParams) { p.WalletID = uuid.Nil }, ErrMissingWalletID},
		{"missing-reference-type", func(p *HoldParams) { p.ReferenceType = "" }, ErrMissingReferenceType},
		{"missing-reference-id", func(p *HoldParams) { p.ReferenceID = "" }, ErrMissingReferenceID},
		{"missing-idempotency", func(p *HoldParams) { p.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"missing-reason", func(p *HoldParams) { p.Reason = "" }, ErrMissingHoldReason},
		{"missing-expiry", func(p *HoldParams) { p.ExpiresAt = time.Time{} }, ErrMissingHoldExpiry},
		{"invalid-amount", func(p *HoldParams) { p.Amount = 0 }, ErrInvalidAmount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := params
			tc.mutate(&p)
			_, err := s.CreateHold(t.Context(), p)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestValidateHoldWalletTarget(t *testing.T) {
	walletID := uuid.New()
	params := HoldParams{
		TenantID: "tenant",
		WalletID: walletID,
	}
	wallet := &Wallet{
		ID:       walletID,
		TenantID: "tenant",
		Status:   WalletStatusActive,
	}
	if err := validateHoldWalletTarget(wallet, params); err != nil {
		t.Fatalf("validateHoldWalletTarget() error = %v", err)
	}

	foreignWallet := *wallet
	foreignWallet.TenantID = "other"
	assertErrorIs(t, validateHoldWalletTarget(&foreignWallet, params), ErrWalletNotFound)

	otherWallet := *wallet
	otherWallet.ID = uuid.New()
	assertErrorIs(t, validateHoldWalletTarget(&otherWallet, params), ErrWalletNotFound)

	closedWallet := *wallet
	closedWallet.Status = WalletStatusClosed
	assertErrorIs(t, validateHoldWalletTarget(&closedWallet, params), ErrWalletInactive)

	assertErrorIs(t, validateHoldWalletTarget(nil, params), ErrWalletNotFound)
}

func TestReleaseHoldValidation(t *testing.T) {
	s := &Store{}
	err := s.ReleaseHold(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	err = s.ReleaseHold(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrInvalidHoldID)
}

func TestUpdateManualTransferStatusValidation(t *testing.T) {
	s := &Store{}
	update := ManualTransferStatusUpdate{Status: "approved"}

	err := s.UpdateManualTransferStatus(t.Context(), "", "wf-1", update)
	assertErrorIs(t, err, ErrMissingTenantID)

	err = s.UpdateManualTransferStatus(t.Context(), "default", "wf-1", update)
	assertErrorIs(t, err, ErrInvalidTenantID)

	err = s.UpdateManualTransferStatus(t.Context(), "tenant", "", update)
	assertErrorIs(t, err, ErrMissingWorkflowID)

	update.Status = ""
	err = s.UpdateManualTransferStatus(t.Context(), "tenant", "wf-1", update)
	assertErrorIs(t, err, ErrMissingStatus)

	update.Status = "bogus"
	err = s.UpdateManualTransferStatus(t.Context(), "tenant", "wf-1", update)
	assertErrorIs(t, err, ErrInvalidStatus)
}

func TestValidateManualTransferStatusTransition(t *testing.T) {
	approvedAt := time.Date(2026, time.May, 31, 12, 0, 0, 0, time.UTC)
	completedAt := approvedAt.Add(time.Hour)
	pending := &ManualTransfer{
		ID:                    11,
		Status:                ManualTransferStatusPending,
		RequestedByOperatorID: 7,
	}
	approved := *pending
	approved.Status = ManualTransferStatusApproved
	approved.ApprovedByOperatorID = sql.NullInt64{Int64: 42, Valid: true}
	approved.ApprovedAt = sql.NullTime{Time: approvedAt, Valid: true}
	approved.ProofOfPayment = sql.NullString{String: "receipt", Valid: true}
	rejected := *pending
	rejected.Status = ManualTransferStatusRejected
	rejected.RejectionReason = sql.NullString{String: "missing documents", Valid: true}
	completed := approved
	completed.Status = ManualTransferStatusCompleted
	completed.CompletedAt = sql.NullTime{Time: completedAt, Valid: true}

	approvalUpdate := ManualTransferStatusUpdate{
		Status:               ManualTransferStatusApproved,
		ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
		ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
		ProofOfPayment:       sql.NullString{String: "receipt", Valid: true},
	}
	rejectionUpdate := ManualTransferStatusUpdate{
		Status:          ManualTransferStatusRejected,
		RejectionReason: sql.NullString{String: "missing documents", Valid: true},
	}
	completionUpdate := ManualTransferStatusUpdate{
		Status:      ManualTransferStatusCompleted,
		CompletedAt: sql.NullTime{Time: completedAt, Valid: true},
	}

	cases := []struct {
		name    string
		current *ManualTransfer
		update  ManualTransferStatusUpdate
		wantErr error
	}{
		{
			name:    "approve pending",
			current: pending,
			update:  approvalUpdate,
		},
		{
			name:    "approve replay",
			current: &approved,
			update:  approvalUpdate,
		},
		{
			name:    "approve missing approver",
			current: pending,
			update: ManualTransferStatusUpdate{
				Status:         ManualTransferStatusApproved,
				ApprovedAt:     sql.NullTime{Time: approvedAt, Valid: true},
				ProofOfPayment: sql.NullString{String: "receipt", Valid: true},
			},
			wantErr: ErrMissingApproverID,
		},
		{
			name:    "approve self approval",
			current: pending,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusApproved,
				ApprovedByOperatorID: sql.NullInt64{Int64: 7, Valid: true},
				ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
				ProofOfPayment:       sql.NullString{String: "receipt", Valid: true},
			},
			wantErr: ErrApproverIsRequester,
		},
		{
			name:    "approve missing approved at",
			current: pending,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusApproved,
				ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
				ProofOfPayment:       sql.NullString{String: "receipt", Valid: true},
			},
			wantErr: ErrMissingApprovalTime,
		},
		{
			name:    "approve missing proof",
			current: pending,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusApproved,
				ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
				ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
			},
			wantErr: ErrMissingProofOfPayment,
		},
		{
			name:    "approve cannot complete",
			current: pending,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusApproved,
				ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
				ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
				CompletedAt:          sql.NullTime{Time: completedAt, Valid: true},
				ProofOfPayment:       sql.NullString{String: "receipt", Valid: true},
			},
			wantErr: ErrInvalidStatus,
		},
		{
			name:    "approve replay mismatch",
			current: &approved,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusApproved,
				ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
				ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
				ProofOfPayment:       sql.NullString{String: "other receipt", Valid: true},
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name:    "reject pending",
			current: pending,
			update:  rejectionUpdate,
		},
		{
			name:    "reject replay",
			current: &rejected,
			update:  rejectionUpdate,
		},
		{
			name:    "reject missing reason",
			current: pending,
			update:  ManualTransferStatusUpdate{Status: ManualTransferStatusRejected},
			wantErr: ErrMissingReason,
		},
		{
			name:    "reject cannot approve",
			current: pending,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusRejected,
				ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
				RejectionReason:      sql.NullString{String: "missing documents", Valid: true},
			},
			wantErr: ErrInvalidStatus,
		},
		{
			name:    "reject replay mismatch",
			current: &rejected,
			update: ManualTransferStatusUpdate{
				Status:          ManualTransferStatusRejected,
				RejectionReason: sql.NullString{String: "different reason", Valid: true},
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name:    "complete approved",
			current: &approved,
			update:  completionUpdate,
		},
		{
			name:    "complete replay",
			current: &completed,
			update:  completionUpdate,
		},
		{
			name:    "complete pending",
			current: pending,
			update:  completionUpdate,
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name:    "complete missing completed at",
			current: &approved,
			update:  ManualTransferStatusUpdate{Status: ManualTransferStatusCompleted},
			wantErr: ErrMissingCompletionTime,
		},
		{
			name: "complete without approval evidence",
			current: &ManualTransfer{
				Status:                ManualTransferStatusApproved,
				RequestedByOperatorID: 7,
			},
			update:  completionUpdate,
			wantErr: ErrMissingApproverID,
		},
		{
			name: "current missing requester",
			current: &ManualTransfer{
				Status: ManualTransferStatusPending,
			},
			update:  rejectionUpdate,
			wantErr: ErrMissingRequesterID,
		},
		{
			name: "current pending has approval evidence",
			current: &ManualTransfer{
				Status:                ManualTransferStatusPending,
				RequestedByOperatorID: 7,
				ProofOfPayment:        sql.NullString{String: "receipt", Valid: true},
			},
			update:  approvalUpdate,
			wantErr: ErrInvalidStatus,
		},
		{
			name: "current rejected has approval evidence",
			current: &ManualTransfer{
				Status:                ManualTransferStatusRejected,
				RequestedByOperatorID: 7,
				ApprovedByOperatorID:  sql.NullInt64{Int64: 42, Valid: true},
				RejectionReason:       sql.NullString{String: "missing documents", Valid: true},
			},
			update:  rejectionUpdate,
			wantErr: ErrInvalidStatus,
		},
		{
			name: "current completed missing completed at",
			current: &ManualTransfer{
				Status:                ManualTransferStatusCompleted,
				RequestedByOperatorID: 7,
				ApprovedByOperatorID:  sql.NullInt64{Int64: 42, Valid: true},
				ApprovedAt:            sql.NullTime{Time: approvedAt, Valid: true},
				ProofOfPayment:        sql.NullString{String: "receipt", Valid: true},
			},
			update:  completionUpdate,
			wantErr: ErrMissingCompletionTime,
		},
		{
			name:    "complete with approval rewrite",
			current: &approved,
			update: ManualTransferStatusUpdate{
				Status:               ManualTransferStatusCompleted,
				ApprovedByOperatorID: sql.NullInt64{Int64: 99, Valid: true},
				CompletedAt:          sql.NullTime{Time: completedAt, Valid: true},
			},
			wantErr: ErrInvalidStatus,
		},
		{
			name:    "complete replay mismatch",
			current: &completed,
			update: ManualTransferStatusUpdate{
				Status:      ManualTransferStatusCompleted,
				CompletedAt: sql.NullTime{Time: completedAt.Add(time.Second), Valid: true},
			},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name:    "pending update rejected",
			current: pending,
			update:  ManualTransferStatusUpdate{Status: ManualTransferStatusPending},
			wantErr: ErrInvalidStatusTransition,
		},
		{
			name:    "nil current",
			current: nil,
			update:  approvalUpdate,
			wantErr: ErrManualTransferNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateManualTransferStatusTransition(tc.current, tc.update)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("ValidateManualTransferStatusTransition() error = %v", err)
				}
				return
			}
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestMergeManualTransferStatusUpdatePreservesApprovalEvidence(t *testing.T) {
	approvedAt := time.Date(2026, time.May, 31, 12, 0, 0, 0, time.UTC)
	completedAt := approvedAt.Add(time.Hour)
	current := &ManualTransfer{
		Status:               ManualTransferStatusApproved,
		ApprovedByOperatorID: sql.NullInt64{Int64: 42, Valid: true},
		ApprovedAt:           sql.NullTime{Time: approvedAt, Valid: true},
		ProofOfPayment:       sql.NullString{String: "receipt", Valid: true},
	}
	merged := mergeManualTransferStatusUpdate(current, ManualTransferStatusUpdate{
		Status:      ManualTransferStatusCompleted,
		CompletedAt: sql.NullTime{Time: completedAt, Valid: true},
	})

	if merged.ApprovedByOperatorID != current.ApprovedByOperatorID {
		t.Fatalf("approved_by = %+v, want %+v", merged.ApprovedByOperatorID, current.ApprovedByOperatorID)
	}
	if !sameManualTransferNullTime(merged.ApprovedAt, current.ApprovedAt) {
		t.Fatalf("approved_at = %+v, want %+v", merged.ApprovedAt, current.ApprovedAt)
	}
	if merged.ProofOfPayment != current.ProofOfPayment {
		t.Fatalf("proof_of_payment = %+v, want %+v", merged.ProofOfPayment, current.ProofOfPayment)
	}
	if !sameManualTransferNullTime(merged.CompletedAt, sql.NullTime{Time: completedAt, Valid: true}) {
		t.Fatalf("completed_at = %+v, want %s", merged.CompletedAt, completedAt)
	}
}

func TestCreateManualTransferValidation(t *testing.T) {
	s := &Store{}
	transfer := ManualTransfer{
		TenantID:               "tenant",
		WorkflowID:             "wf-1",
		IdempotencyKey:         "idem-1",
		TransferType:           ManualTransferTypeDebit,
		WalletID:               sql.NullString{String: uuid.NewString(), Valid: true},
		Amount:                 100,
		Currency:               "USD",
		Reason:                 "withdrawal",
		Status:                 "pending",
		RequestedByOperatorID:  42,
		ApprovalTimeoutSeconds: 3600,
	}

	_, err := s.CreateManualTransfer(t.Context(), ManualTransfer{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := transfer
	bad.TenantID = "default"
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = transfer
	bad.WorkflowID = ""
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingWorkflowID)

	bad = transfer
	bad.IdempotencyKey = ""
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingIdempotencyKey)

	bad = transfer
	bad.TransferType = ""
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingTransferType)

	bad = transfer
	bad.TransferType = "bank_transfer"
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTransferType)

	bad = transfer
	bad.Amount = 0
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidAmount)

	bad = transfer
	bad.Currency = ""
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingCurrency)

	bad = transfer
	bad.Reason = ""
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingReason)

	bad = transfer
	bad.Status = ""
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingStatus)

	bad = transfer
	bad.Status = "bogus"
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidStatus)

	bad = transfer
	bad.Status = ManualTransferStatusApproved
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidStatus)

	bad = transfer
	bad.ApprovedByOperatorID = sql.NullInt64{Int64: 2, Valid: true}
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidStatus)

	bad = transfer
	bad.ProofOfPayment = sql.NullString{String: "proof", Valid: true}
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidStatus)

	bad = transfer
	bad.WalletID = sql.NullString{}
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingWalletID)

	bad = transfer
	bad.WalletID = sql.NullString{String: "not-a-uuid", Valid: true}
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingWalletID)

	bad = transfer
	bad.RequestedByOperatorID = 0
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingRequesterID)

	bad = transfer
	bad.ApprovalTimeoutSeconds = 0
	_, err = s.CreateManualTransfer(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingApprovalTimeout)
}

func TestValidateManualTransferCreateReplay(t *testing.T) {
	requested := ManualTransfer{
		TenantID:               "tenant",
		WorkflowID:             "wf-1",
		IdempotencyKey:         "idem-1",
		TransferType:           ManualTransferTypeDebit,
		WalletID:               sql.NullString{String: uuid.NewString(), Valid: true},
		Amount:                 100,
		Currency:               "USD",
		Reason:                 "withdrawal",
		Status:                 ManualTransferStatusPending,
		RequestedByOperatorID:  42,
		PSPProvider:            sql.NullString{String: "bank", Valid: true},
		PSPReference:           sql.NullString{String: "ref-1", Valid: true},
		ApprovalTimeoutSeconds: 3600,
		DecisionDeadlineAt:     time.Now().UTC().Add(time.Hour),
	}
	existing := requested
	existing.Status = ManualTransferStatusCompleted
	if err := ValidateManualTransferCreateReplay(&existing, requested); err != nil {
		t.Fatalf("ValidateManualTransferCreateReplay() error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ManualTransfer)
	}{
		{"tenant", func(t *ManualTransfer) { t.TenantID = "other" }},
		{"workflow", func(t *ManualTransfer) { t.WorkflowID = "other" }},
		{"idempotency", func(t *ManualTransfer) { t.IdempotencyKey = "other" }},
		{"type", func(t *ManualTransfer) { t.TransferType = ManualTransferTypeCredit }},
		{"wallet", func(t *ManualTransfer) { t.WalletID = sql.NullString{String: uuid.NewString(), Valid: true} }},
		{"amount", func(t *ManualTransfer) { t.Amount++ }},
		{"currency", func(t *ManualTransfer) { t.Currency = "AED" }},
		{"reason", func(t *ManualTransfer) { t.Reason = "other" }},
		{"requested-by", func(t *ManualTransfer) { t.RequestedByOperatorID = 99 }},
		{"psp-provider", func(t *ManualTransfer) { t.PSPProvider = sql.NullString{String: "other", Valid: true} }},
		{"psp-reference", func(t *ManualTransfer) { t.PSPReference = sql.NullString{String: "other", Valid: true} }},
		{"approval-timeout", func(t *ManualTransfer) { t.ApprovalTimeoutSeconds++ }},
	}
	retried := requested
	retried.DecisionDeadlineAt = retried.DecisionDeadlineAt.Add(time.Hour)
	if err := ValidateManualTransferCreateReplay(&existing, retried); err != nil {
		t.Fatalf("same timeout retry with regenerated deadline: %v", err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay := requested
			tc.mutate(&replay)
			assertErrorIs(t, ValidateManualTransferCreateReplay(&existing, replay), ErrDuplicateManualTransfer)
		})
	}
	assertErrorIs(t, ValidateManualTransferCreateReplay(nil, requested), ErrDuplicateManualTransfer)
}

func TestValidateManualTransferCreateTarget(t *testing.T) {
	walletID := uuid.New()
	wallet := &Wallet{
		ID:       walletID,
		TenantID: "tenant",
		Currency: "USD",
		Status:   WalletStatusActive,
	}
	requester := &OperatorIdentity{ID: 42, Issuer: "https://identity.example/realms/noebs", Subject: "requester"}
	transfer := ManualTransfer{
		TenantID:              "tenant",
		WalletID:              sql.NullString{String: walletID.String(), Valid: true},
		Currency:              "USD",
		RequestedByOperatorID: requester.ID,
	}
	if err := ValidateManualTransferCreateTarget(wallet, requester, transfer); err != nil {
		t.Fatalf("ValidateManualTransferCreateTarget() error = %v", err)
	}

	foreignWallet := *wallet
	foreignWallet.TenantID = "other"
	assertErrorIs(t, ValidateManualTransferCreateTarget(&foreignWallet, requester, transfer), ErrWalletNotFound)

	otherWallet := *wallet
	otherWallet.ID = uuid.New()
	assertErrorIs(t, ValidateManualTransferCreateTarget(&otherWallet, requester, transfer), ErrWalletNotFound)

	inactiveWallet := *wallet
	inactiveWallet.Status = WalletStatusFrozen
	assertErrorIs(t, ValidateManualTransferCreateTarget(&inactiveWallet, requester, transfer), ErrWalletInactive)

	otherCurrency := *wallet
	otherCurrency.Currency = "AED"
	assertErrorIs(t, ValidateManualTransferCreateTarget(&otherCurrency, requester, transfer), ErrCurrencyMismatch)

	otherRequester := *requester
	otherRequester.ID = 99
	assertErrorIs(t, ValidateManualTransferCreateTarget(wallet, &otherRequester, transfer), ErrOperatorIdentityNotFound)

	missingRequester := transfer
	missingRequester.RequestedByOperatorID = 0
	assertErrorIs(t, ValidateManualTransferCreateTarget(wallet, requester, missingRequester), ErrMissingRequesterID)

	assertErrorIs(t, ValidateManualTransferCreateTarget(nil, requester, transfer), ErrWalletNotFound)
	assertErrorIs(t, ValidateManualTransferCreateTarget(wallet, nil, transfer), ErrOperatorIdentityNotFound)
}

func TestAddManualTransferApprovalValidation(t *testing.T) {
	s := &Store{}
	approval := ManualTransferApproval{
		TenantID:            "tenant",
		ManualTransferID:    1,
		DecidedByOperatorID: 2,
		Decision:            "approved",
	}

	_, err := s.AddManualTransferApproval(t.Context(), ManualTransferApproval{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := approval
	bad.TenantID = "default"
	_, err = s.AddManualTransferApproval(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = approval
	bad.ManualTransferID = 0
	_, err = s.AddManualTransferApproval(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingManualTransferID)

	bad = approval
	bad.DecidedByOperatorID = 0
	_, err = s.AddManualTransferApproval(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingApproverID)

	bad = approval
	bad.Decision = ""
	_, err = s.AddManualTransferApproval(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingDecision)

	bad = approval
	bad.Decision = "bogus"
	_, err = s.AddManualTransferApproval(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidDecision)
}

func TestValidateManualTransferApprovalReplay(t *testing.T) {
	requested := ManualTransferApproval{
		TenantID:            "tenant",
		ManualTransferID:    1,
		DecidedByOperatorID: 42,
		Decision:            ManualTransferStatusApproved,
		Reason:              sql.NullString{String: "ok", Valid: true},
	}
	if err := ValidateManualTransferApprovalReplay(&requested, requested); err != nil {
		t.Fatalf("ValidateManualTransferApprovalReplay() error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ManualTransferApproval)
	}{
		{"tenant", func(a *ManualTransferApproval) { a.TenantID = "other" }},
		{"transfer", func(a *ManualTransferApproval) { a.ManualTransferID = 2 }},
		{"approver", func(a *ManualTransferApproval) { a.DecidedByOperatorID = 99 }},
		{"decision", func(a *ManualTransferApproval) { a.Decision = ManualTransferStatusRejected }},
		{"reason", func(a *ManualTransferApproval) { a.Reason = sql.NullString{String: "other", Valid: true} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay := requested
			tc.mutate(&replay)
			assertErrorIs(t, ValidateManualTransferApprovalReplay(&requested, replay), ErrDuplicateManualApproval)
		})
	}
	assertErrorIs(t, ValidateManualTransferApprovalReplay(nil, requested), ErrDuplicateManualApproval)
}

func TestValidateManualTransferApprovalTarget(t *testing.T) {
	transfer := &ManualTransfer{
		ID:                    11,
		TenantID:              "tenant",
		Status:                ManualTransferStatusPending,
		RequestedByOperatorID: 7,
	}
	approver := &OperatorIdentity{ID: 42, Issuer: "https://identity.example/realms/noebs", Subject: "approver"}
	approval := ManualTransferApproval{
		TenantID:            "tenant",
		ManualTransferID:    11,
		DecidedByOperatorID: 42,
		Decision:            ManualTransferStatusApproved,
	}
	if err := ValidateManualTransferApprovalTarget(transfer, approver, approval); err != nil {
		t.Fatalf("ValidateManualTransferApprovalTarget() error = %v", err)
	}

	foreignTransfer := *transfer
	foreignTransfer.TenantID = "other"
	assertErrorIs(t, ValidateManualTransferApprovalTarget(&foreignTransfer, approver, approval), ErrManualTransferNotFound)

	otherTransfer := *transfer
	otherTransfer.ID = 12
	assertErrorIs(t, ValidateManualTransferApprovalTarget(&otherTransfer, approver, approval), ErrManualTransferNotFound)

	terminalTransfer := *transfer
	terminalTransfer.Status = ManualTransferStatusApproved
	assertErrorIs(t, ValidateManualTransferApprovalTarget(&terminalTransfer, approver, approval), ErrInvalidStatusTransition)

	selfApproval := approval
	selfApproval.DecidedByOperatorID = transfer.RequestedByOperatorID
	requester := *approver
	requester.ID = transfer.RequestedByOperatorID
	assertErrorIs(t, ValidateManualTransferApprovalTarget(transfer, &requester, selfApproval), ErrApproverIsRequester)

	assertErrorIs(t, ValidateManualTransferApprovalTarget(nil, approver, approval), ErrManualTransferNotFound)
	assertErrorIs(t, ValidateManualTransferApprovalTarget(transfer, nil, approval), ErrOperatorIdentityNotFound)
}

func TestGetManualTransferByWorkflowValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetManualTransferByWorkflow(t.Context(), "", "wf-1")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetManualTransferByWorkflow(t.Context(), "default", "wf-1")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetManualTransferByWorkflow(t.Context(), "tenant", "")
	assertErrorIs(t, err, ErrMissingWorkflowID)
}

func TestListManualTransfersValidation(t *testing.T) {
	s := &Store{}
	filter := ManualTransferFilter{TenantID: "", Limit: 10}
	_, err := s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrMissingTenantID)

	filter = ManualTransferFilter{TenantID: "default", Limit: 10}
	_, err = s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrInvalidTenantID)

	filter = ManualTransferFilter{TenantID: "tenant", Limit: 0}
	_, err = s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrInvalidLimit)

	filter = ManualTransferFilter{TenantID: "tenant", Limit: 10, Offset: -1}
	_, err = s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrInvalidOffset)

	now := time.Now().UTC()
	filter = ManualTransferFilter{TenantID: "tenant", Limit: 10, Offset: 0, Start: now}
	_, err = s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrMissingEndTime)

	filter = ManualTransferFilter{TenantID: "tenant", Limit: 10, Offset: 0, End: now}
	_, err = s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrMissingStartTime)

	filter = ManualTransferFilter{TenantID: "tenant", Limit: 10, Offset: 0, Start: now.Add(time.Hour), End: now}
	_, err = s.ListManualTransfers(t.Context(), filter)
	assertErrorIs(t, err, ErrInvalidTimeRange)
}

func TestListManualTransferApprovalsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListManualTransferApprovals(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListManualTransferApprovals(t.Context(), "default", 1)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListManualTransferApprovals(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrMissingManualTransferID)
}

func TestCreatePSPTransactionValidation(t *testing.T) {
	s := &Store{}
	base := PSPTransaction{
		TenantID:        "tenant",
		PSPProvider:     "coinsbuy",
		IdempotencyKey:  "idem-1",
		ClientReference: "ref-1",
		Direction:       "inbound",
		Amount:          100,
		Currency:        "USD",
		Status:          "initiated",
	}

	cases := []struct {
		name    string
		mutate  func(txn *PSPTransaction)
		wantErr error
	}{
		{"missing-tenant", func(txn *PSPTransaction) { txn.TenantID = "" }, ErrMissingTenantID},
		{"invalid-tenant", func(txn *PSPTransaction) { txn.TenantID = "default" }, ErrInvalidTenantID},
		{"missing-provider", func(txn *PSPTransaction) { txn.PSPProvider = "" }, ErrMissingProviderCode},
		{"missing-idempotency", func(txn *PSPTransaction) { txn.IdempotencyKey = "" }, ErrMissingIdempotencyKey},
		{"missing-reference", func(txn *PSPTransaction) { txn.ClientReference = "" }, ErrMissingClientReference},
		{"missing-direction", func(txn *PSPTransaction) { txn.Direction = "" }, ErrMissingDirection},
		{"invalid-amount", func(txn *PSPTransaction) { txn.Amount = 0 }, ErrInvalidAmount},
		{"missing-currency", func(txn *PSPTransaction) { txn.Currency = "" }, ErrMissingCurrency},
		{"missing-status", func(txn *PSPTransaction) { txn.Status = "" }, ErrMissingStatus},
		{"invalid-status", func(txn *PSPTransaction) { txn.Status = "complete" }, ErrInvalidStatus},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			txn := base
			tc.mutate(&txn)
			_, err := s.CreatePSPTransaction(t.Context(), txn)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestValidatePSPStatusTransition(t *testing.T) {
	if err := ValidatePSPStatusTransition(PSPStatusPending, PSPStatusSuccess); err != nil {
		t.Fatalf("ValidatePSPStatusTransition(nonterminal) error = %v", err)
	}
	if err := ValidatePSPStatusTransition(PSPStatusSuccess, PSPStatusSuccess); err != nil {
		t.Fatalf("ValidatePSPStatusTransition(replay) error = %v", err)
	}
	assertErrorIs(t, ValidatePSPStatusTransition("", PSPStatusSuccess), ErrMissingStatus)
	assertErrorIs(t, ValidatePSPStatusTransition(PSPStatusSuccess, "complete"), ErrInvalidStatus)
	assertErrorIs(t, ValidatePSPStatusTransition(PSPStatusSuccess, PSPStatusFailed), ErrInvalidStatusTransition)
	assertErrorIs(t, ValidatePSPStatusTransition(PSPStatusFailed, PSPStatusPending), ErrInvalidStatusTransition)
	assertErrorIs(t, ValidatePSPStatusTransition(PSPStatusPending, PSPStatusInitiated), ErrInvalidStatusTransition)
	assertErrorIs(t, ValidatePSPStatusTransition(PSPStatusHeld, PSPStatusInitiated), ErrInvalidStatusTransition)
	if err := ValidatePSPStatusTransition(PSPStatusHeld, PSPStatusProcessing); err != nil {
		t.Fatalf("held polling transition: %v", err)
	}
}

func TestValidatePSPStatusUpdate(t *testing.T) {
	existing := &PSPTransaction{
		Status:           PSPStatusPending,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		Amount:           100,
		Currency:         "USD",
	}
	if err := ValidatePSPStatusUpdate(existing, PSPStatusUpdate{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
	}); err != nil {
		t.Fatalf("ValidatePSPStatusUpdate() error = %v", err)
	}
	if err := ValidatePSPStatusUpdate(&PSPTransaction{Status: PSPStatusPending}, PSPStatusUpdate{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
	}); err != nil {
		t.Fatalf("ValidatePSPStatusUpdate() fill provider id error = %v", err)
	}
	assertErrorIs(t, ValidatePSPStatusUpdate(nil, PSPStatusUpdate{Status: PSPStatusSuccess}), ErrPSPTransactionNotFound)
	assertErrorIs(t, ValidatePSPStatusUpdate(existing, PSPStatusUpdate{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-2", Valid: true},
	}), ErrDuplicateTransaction)
	assertErrorIs(t, ValidatePSPStatusUpdate(&PSPTransaction{Status: PSPStatusSuccess}, PSPStatusUpdate{Status: PSPStatusPending}), ErrInvalidStatusTransition)

	confirmedAt := time.Now().UTC().Truncate(time.Microsecond)
	terminal := &PSPTransaction{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ResponseCode:     sql.NullString{String: "00", Valid: true},
		ResponseMessage:  sql.NullString{String: "ok", Valid: true},
		RawResponse:      RawJSON(`{"provider_id":"psp-1","status":"success"}`),
		ConfirmedAt:      sql.NullTime{Time: confirmedAt, Valid: true},
	}
	if err := ValidatePSPStatusUpdate(terminal, PSPStatusUpdate{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		ResponseCode:     sql.NullString{String: "00", Valid: true},
		ResponseMessage:  sql.NullString{String: "ok", Valid: true},
		RawResponse:      RawJSON(`{"status":"success","provider_id":"psp-1"}`),
		ConfirmedAt:      sql.NullTime{Time: confirmedAt, Valid: true},
	}); err != nil {
		t.Fatalf("ValidatePSPStatusUpdate(terminal replay) error = %v", err)
	}
	assertErrorIs(t, ValidatePSPStatusUpdate(terminal, PSPStatusUpdate{
		Status:       PSPStatusSuccess,
		ResponseCode: sql.NullString{String: "05", Valid: true},
	}), ErrDuplicateTransaction)
	assertErrorIs(t, ValidatePSPStatusUpdate(terminal, PSPStatusUpdate{
		Status:          PSPStatusSuccess,
		ResponseMessage: sql.NullString{String: "changed", Valid: true},
	}), ErrDuplicateTransaction)
	assertErrorIs(t, ValidatePSPStatusUpdate(terminal, PSPStatusUpdate{
		Status:      PSPStatusSuccess,
		RawResponse: RawJSON(`{"provider_id":"psp-1","status":"success","amount":101}`),
	}), ErrDuplicateTransaction)
	assertErrorIs(t, ValidatePSPStatusUpdate(terminal, PSPStatusUpdate{
		Status:      PSPStatusSuccess,
		ConfirmedAt: sql.NullTime{Time: confirmedAt.Add(time.Second), Valid: true},
	}), ErrDuplicateTransaction)

	if err := ValidatePSPStatusUpdate(&PSPTransaction{Status: PSPStatusSuccess}, PSPStatusUpdate{
		Status:       PSPStatusSuccess,
		ResponseCode: sql.NullString{String: "00", Valid: true},
	}); err != nil {
		t.Fatalf("ValidatePSPStatusUpdate(fill terminal details) error = %v", err)
	}
}

func TestPSPWorkflowSignalContract(t *testing.T) {
	existing := &PSPTransaction{
		Status:           PSPStatusPending,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		Amount:           100,
		Currency:         "USD",
	}
	update := PSPStatusUpdate{
		Status:           PSPStatusSuccess,
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		RawResponse:      RawJSON(`{"status":"success"}`),
	}
	signal := &PSPWorkflowSignal{
		ProviderTxID: "psp-1",
		Amount:       100,
		Currency:     "USD",
		Status:       PSPStatusSuccess,
		RawResponse:  RawJSON(`{"status":"success"}`),
	}
	payload, err := encodePSPWorkflowSignal(existing, update, signal)
	if err != nil {
		t.Fatalf("encode workflow signal: %v", err)
	}
	parsed, err := ParsePSPWorkflowSignal(payload)
	if err != nil {
		t.Fatalf("parse workflow signal: %v", err)
	}
	if parsed.ProviderTxID != signal.ProviderTxID || parsed.Amount != signal.Amount || parsed.Currency != signal.Currency || parsed.Status != signal.Status {
		t.Fatalf("parsed workflow signal = %+v, want %+v", parsed, *signal)
	}

	wrongStatus := *signal
	wrongStatus.Status = PSPStatusFailed
	if _, err := encodePSPWorkflowSignal(existing, update, &wrongStatus); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("wrong status signal error = %v, want %v", err, ErrInvalidStatusTransition)
	}
	wrongProviderID := *signal
	wrongProviderID.ProviderTxID = "psp-2"
	if _, err := encodePSPWorkflowSignal(existing, update, &wrongProviderID); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("wrong provider id signal error = %v, want %v", err, ErrDuplicateTransaction)
	}
	wrongResponse := *signal
	wrongResponse.RawResponse = RawJSON(`{"status":"success","changed":true}`)
	if _, err := encodePSPWorkflowSignal(existing, update, &wrongResponse); !errors.Is(err, ErrDuplicateTransaction) {
		t.Fatalf("wrong raw response signal error = %v, want %v", err, ErrDuplicateTransaction)
	}
	wrongAmount := *signal
	wrongAmount.Amount++
	if _, err := encodePSPWorkflowSignal(existing, update, &wrongAmount); !errors.Is(err, ErrInvalidAmount) {
		t.Fatalf("wrong amount signal error = %v, want %v", err, ErrInvalidAmount)
	}
	wrongCurrency := *signal
	wrongCurrency.Currency = "AED"
	if _, err := encodePSPWorkflowSignal(existing, update, &wrongCurrency); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("wrong currency signal error = %v, want %v", err, ErrCurrencyMismatch)
	}
	if _, err := ParsePSPWorkflowSignal(RawJSON(`{"status":"pending"}`)); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("nonterminal durable signal error = %v, want %v", err, ErrInvalidStatusTransition)
	}
}

func TestValidatePSPTransactionCreateReplay(t *testing.T) {
	requested := PSPTransaction{
		TenantID:         "tenant",
		PSPProvider:      "coinsbuy",
		PSPTransactionID: sql.NullString{String: "psp-1", Valid: true},
		IdempotencyKey:   "idem-1",
		ClientReference:  "ref-1",
		Direction:        "inbound",
		Amount:           100,
		FeeAmount:        sql.NullInt64{Int64: 5, Valid: true},
		NetAmount:        sql.NullInt64{Int64: 95, Valid: true},
		Currency:         "USD",
		Status:           "initiated",
		WorkflowID:       sql.NullString{String: "workflow-1", Valid: true},
		RawRequest:       RawJSON(`{"client_reference":"ref-1","amount":100}`),
	}
	existing := requested
	existing.Status = "success"
	if err := ValidatePSPTransactionCreateReplay(&existing, requested); err != nil {
		t.Fatalf("ValidatePSPTransactionCreateReplay() error = %v, want nil", err)
	}

	requestWithoutProviderTxn := requested
	requestWithoutProviderTxn.PSPTransactionID = sql.NullString{}
	existingWithProviderTxn := existing
	existingWithProviderTxn.PSPTransactionID = sql.NullString{String: "provider-confirmed", Valid: true}
	if err := ValidatePSPTransactionCreateReplay(&existingWithProviderTxn, requestWithoutProviderTxn); err != nil {
		t.Fatalf("ValidatePSPTransactionCreateReplay() with later provider id error = %v, want nil", err)
	}

	cases := []struct {
		name   string
		mutate func(*PSPTransaction)
	}{
		{"provider", func(txn *PSPTransaction) { txn.PSPProvider = "other" }},
		{"idempotency", func(txn *PSPTransaction) { txn.IdempotencyKey = "other" }},
		{"direction", func(txn *PSPTransaction) { txn.Direction = "outbound" }},
		{"amount", func(txn *PSPTransaction) { txn.Amount++ }},
		{"fee", func(txn *PSPTransaction) { txn.FeeAmount = sql.NullInt64{} }},
		{"currency", func(txn *PSPTransaction) { txn.Currency = "AED" }},
		{"workflow", func(txn *PSPTransaction) { txn.WorkflowID = sql.NullString{String: "other", Valid: true} }},
		{"raw request", func(txn *PSPTransaction) { txn.RawRequest = RawJSON(`{"client_reference":"ref-1","amount":101}`) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			replay := requested
			tc.mutate(&replay)
			err := ValidatePSPTransactionCreateReplay(&existing, replay)
			assertErrorIs(t, err, ErrDuplicateTransaction)
		})
	}
}

func TestRecordPSPInteractionValidation(t *testing.T) {
	s := &Store{}
	base := PSPInteraction{
		TenantID:        "tenant",
		PSPProvider:     "coinsbuy",
		InteractionType: "status_check",
	}

	cases := []struct {
		name    string
		mutate  func(interaction *PSPInteraction)
		wantErr error
	}{
		{"missing-tenant", func(interaction *PSPInteraction) { interaction.TenantID = "" }, ErrMissingTenantID},
		{"invalid-tenant", func(interaction *PSPInteraction) { interaction.TenantID = "default" }, ErrInvalidTenantID},
		{"missing-provider", func(interaction *PSPInteraction) { interaction.PSPProvider = "" }, ErrMissingProviderCode},
		{"missing-interaction-type", func(interaction *PSPInteraction) { interaction.InteractionType = "" }, ErrMissingInteractionType},
		{"dispatch-missing-idempotency", func(interaction *PSPInteraction) { interaction.InteractionType = "payout_send" }, ErrMissingIdempotencyKey},
		{"non-dispatch-idempotency", func(interaction *PSPInteraction) {
			interaction.IdempotencyKey = sql.NullString{String: "ref-1", Valid: true}
		}, ErrInvalidIdempotencyKey},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			interaction := base
			tc.mutate(&interaction)
			_, err := s.RecordPSPInteraction(t.Context(), interaction)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestGetPSPTransactionByReferenceValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetPSPTransactionByReference(t.Context(), "", "ref-1")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetPSPTransactionByReference(t.Context(), "default", "ref-1")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetPSPTransactionByReference(t.Context(), "tenant", "")
	assertErrorIs(t, err, ErrMissingClientReference)
}

func TestUpdatePSPTransactionStatusValidation(t *testing.T) {
	s := &Store{}
	update := PSPStatusUpdate{Status: "success"}

	err := s.UpdatePSPTransactionStatus(t.Context(), "", "ref-1", update)
	assertErrorIs(t, err, ErrMissingTenantID)

	err = s.UpdatePSPTransactionStatus(t.Context(), "default", "ref-1", update)
	assertErrorIs(t, err, ErrInvalidTenantID)

	err = s.UpdatePSPTransactionStatus(t.Context(), "tenant", "", update)
	assertErrorIs(t, err, ErrMissingClientReference)

	update.Status = ""
	err = s.UpdatePSPTransactionStatus(t.Context(), "tenant", "ref-1", update)
	assertErrorIs(t, err, ErrMissingStatus)

	update.Status = "complete"
	err = s.UpdatePSPTransactionStatus(t.Context(), "tenant", "ref-1", update)
	assertErrorIs(t, err, ErrInvalidStatus)
}

func TestListPSPTransactionsForPollingValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactionsForPolling(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionsForPolling(t.Context(), "default", 1)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListPSPTransactionsForPolling(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrInvalidLimit)
}

func TestListPSPTransactionsByStatusValidation(t *testing.T) {
	s := &Store{}
	start := time.Now().UTC().Add(-time.Hour)
	end := time.Now().UTC()

	_, err := s.ListPSPTransactionsByStatus(t.Context(), "", "success", start, end, 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "default", "success", start, end, 1)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "tenant", "", start, end, 1)
	assertErrorIs(t, err, ErrMissingStatus)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "tenant", "complete", start, end, 1)
	assertErrorIs(t, err, ErrInvalidStatus)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "tenant", "success", time.Time{}, end, 1)
	assertErrorIs(t, err, ErrMissingStartTime)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "tenant", "success", start, time.Time{}, 1)
	assertErrorIs(t, err, ErrMissingEndTime)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "tenant", "success", end, start, 1)
	assertErrorIs(t, err, ErrInvalidTimeRange)

	_, err = s.ListPSPTransactionsByStatus(t.Context(), "tenant", "success", start, end, 0)
	assertErrorIs(t, err, ErrInvalidLimit)
}

func TestTryAcquirePSPTransactionLockValidation(t *testing.T) {
	s := &Store{}
	now := time.Now().UTC()

	_, err := s.TryAcquirePSPTransactionLock(t.Context(), "", "ref-1", "token", now)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.TryAcquirePSPTransactionLock(t.Context(), "default", "ref-1", "token", now)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.TryAcquirePSPTransactionLock(t.Context(), "tenant", "", "token", now)
	assertErrorIs(t, err, ErrMissingClientReference)

	_, err = s.TryAcquirePSPTransactionLock(t.Context(), "tenant", "ref-1", "", now)
	assertErrorIs(t, err, ErrMissingLockToken)

	_, err = s.TryAcquirePSPTransactionLock(t.Context(), "tenant", "ref-1", "token", time.Time{})
	assertErrorIs(t, err, ErrMissingLockExpiry)
}

func TestLedgerTransactionExistsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.LedgerTransactionExists(t.Context(), "", "idem-1")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.LedgerTransactionExists(t.Context(), "default", "idem-1")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.LedgerTransactionExists(t.Context(), "tenant", "")
	assertErrorIs(t, err, ErrMissingIdempotencyKey)
}

func TestLedgerTransactionExistsByReferenceValidation(t *testing.T) {
	s := &Store{}
	_, err := s.LedgerTransactionExistsByReference(t.Context(), "", "deposit", "ref-1")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.LedgerTransactionExistsByReference(t.Context(), "default", "deposit", "ref-1")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.LedgerTransactionExistsByReference(t.Context(), "tenant", "", "ref-1")
	assertErrorIs(t, err, ErrMissingReferenceType)

	_, err = s.LedgerTransactionExistsByReference(t.Context(), "tenant", "deposit", "")
	assertErrorIs(t, err, ErrMissingReferenceID)
}

func TestCreateWithdrawalDestinationLinkValidation(t *testing.T) {
	s := &Store{}
	link := LedgerWithdrawalDestinationLink{
		TenantID:      "tenant",
		LedgerEntryID: 1,
		DestinationID: 2,
		Amount:        100,
		Currency:      "USD",
	}

	_, err := s.CreateWithdrawalDestinationLink(t.Context(), LedgerWithdrawalDestinationLink{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := link
	bad.TenantID = "default"
	_, err = s.CreateWithdrawalDestinationLink(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = link
	bad.LedgerEntryID = 0
	_, err = s.CreateWithdrawalDestinationLink(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingLedgerEntryID)

	bad = link
	bad.DestinationID = 0
	_, err = s.CreateWithdrawalDestinationLink(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingDestinationID)

	bad = link
	bad.Amount = 0
	_, err = s.CreateWithdrawalDestinationLink(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidAmount)

	bad = link
	bad.Currency = ""
	_, err = s.CreateWithdrawalDestinationLink(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingCurrency)
}

func TestGetFundingSourceByIDValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetFundingSourceByID(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetFundingSourceByID(t.Context(), "default", 1)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetFundingSourceByID(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrMissingFundingSourceID)
}

func TestWithdrawalDestinationValidation(t *testing.T) {
	s := &Store{}
	dest := WithdrawalDestination{
		TenantID:              "tenant",
		WalletID:              uuid.New(),
		DestinationType:       "bank_account",
		Currency:              "USD",
		DestinationDetails:    []byte(`{"account":"123"}`),
		LinkedFundingSourceID: 1,
	}

	_, err := s.CreateWithdrawalDestination(t.Context(), WithdrawalDestination{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := dest
	bad.TenantID = "default"
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = dest
	bad.WalletID = uuid.Nil
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingWalletID)

	bad = dest
	bad.DestinationType = ""
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingDestinationType)

	bad = dest
	bad.Currency = ""
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingCurrency)

	bad = dest
	bad.DestinationDetails = nil
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingDestinationDetails)

	bad = dest
	bad.LinkedFundingSourceID = 0
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingFundingSourceID)

	bad = dest
	bad.TotalWithdrawn = 100
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidAmount)

	bad = dest
	bad.LastUsedAt = sql.NullTime{Time: time.Now().UTC(), Valid: true}
	_, err = s.CreateWithdrawalDestination(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidUsageTime)

	_, err = s.GetWithdrawalDestination(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetWithdrawalDestination(t.Context(), "default", 1)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetWithdrawalDestination(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrMissingDestinationID)

	_, err = s.ListWithdrawalDestinations(t.Context(), "", uuid.New(), true)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListWithdrawalDestinations(t.Context(), "default", uuid.New(), true)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListWithdrawalDestinations(t.Context(), "tenant", uuid.Nil, true)
	assertErrorIs(t, err, ErrMissingWalletID)
}

func TestValidateWithdrawalDestinationFundingSource(t *testing.T) {
	walletID := uuid.New()
	dest := WithdrawalDestination{
		TenantID:              "tenant",
		WalletID:              walletID,
		DestinationType:       "bank_account",
		PSPProvider:           sql.NullString{String: "pay", Valid: true},
		DestinationDetails:    []byte(`{"account_number":"1234567890"}`),
		Currency:              "AED",
		LinkedFundingSourceID: 10,
	}
	source := FundingSource{
		ID:                 10,
		TenantID:           "tenant",
		WalletID:           walletID,
		SourceType:         "bank_account",
		PSPProvider:        sql.NullString{String: "pay", Valid: true},
		Currency:           "AED",
		VerificationStatus: "verified",
		VerifiedAt:         sql.NullTime{Time: time.Now().UTC(), Valid: true},
		SupportsWithdrawal: true,
		WithdrawalMethod:   []byte(`{"account_number":"1234567890"}`),
	}
	if err := ValidateWithdrawalDestinationFundingSource(dest, &source); err != nil {
		t.Fatalf("ValidateWithdrawalDestinationFundingSource() error = %v", err)
	}

	missingLink := dest
	missingLink.LinkedFundingSourceID = 0
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(missingLink, nil), ErrMissingFundingSourceID)

	otherWallet := source
	otherWallet.WalletID = uuid.New()
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &otherWallet), ErrFundingSourceNotFound)

	otherCurrency := source
	otherCurrency.Currency = "USD"
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &otherCurrency), ErrCurrencyMismatch)

	pending := source
	pending.VerificationStatus = "pending"
	pending.VerifiedAt = sql.NullTime{}
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &pending), ErrFundingSourceNotVerified)

	missingVerificationTime := source
	missingVerificationTime.VerifiedAt = sql.NullTime{}
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &missingVerificationTime), ErrMissingVerificationTime)

	notWithdrawable := source
	notWithdrawable.SupportsWithdrawal = false
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &notWithdrawable), ErrFundingSourceNotWithdrawable)

	missingMethod := source
	missingMethod.WithdrawalMethod = nil
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &missingMethod), ErrFundingSourceNotWithdrawable)

	missingProvider := source
	missingProvider.PSPProvider = sql.NullString{}
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(dest, &missingProvider), ErrMissingProviderCode)

	substitutedDetails := dest
	substitutedDetails.DestinationDetails = []byte(`{"account_number":"attacker"}`)
	assertErrorIs(t, ValidateWithdrawalDestinationFundingSource(substitutedDetails, &source), ErrDestinationNotVerified)
}

func TestFundingSourceValidation(t *testing.T) {
	s := &Store{}
	source := FundingSource{
		TenantID:           "tenant",
		WalletID:           uuid.New(),
		SourceType:         "card",
		VerificationStatus: "verified",
		VerifiedAt:         sql.NullTime{Time: time.Now().UTC(), Valid: true},
		Currency:           "USD",
		SourceDetails:      []byte(`{}`),
	}

	_, err := s.UpsertFundingSource(t.Context(), FundingSource{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := source
	bad.TenantID = "default"
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = source
	bad.WalletID = uuid.Nil
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingWalletID)

	bad = source
	bad.SourceType = ""
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingSourceType)

	bad = source
	bad.VerificationStatus = ""
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingStatus)

	bad = source
	bad.VerificationStatus = "complete"
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidStatus)

	bad = source
	bad.VerifiedAt = sql.NullTime{}
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingVerificationTime)

	bad = source
	bad.VerifiedAt = sql.NullTime{Valid: true}
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingVerificationTime)

	bad = source
	bad.VerificationStatus = "pending"
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidVerificationTime)

	bad = source
	bad.Currency = ""
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingCurrency)

	bad = source
	bad.SourceDetails = nil
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingSourceDetails)

	bad = source
	bad.TotalFunded = 100
	_, err = s.UpsertFundingSource(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidAmount)

	_, err = s.GetFundingSource(t.Context(), "", uuid.New(), "card", sql.NullString{})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetFundingSource(t.Context(), "default", uuid.New(), "card", sql.NullString{})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetFundingSource(t.Context(), "tenant", uuid.Nil, "card", sql.NullString{})
	assertErrorIs(t, err, ErrMissingWalletID)

	_, err = s.GetFundingSource(t.Context(), "tenant", uuid.New(), "", sql.NullString{})
	assertErrorIs(t, err, ErrMissingSourceType)

	_, err = s.ListFundingSources(t.Context(), "", uuid.New())
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListFundingSources(t.Context(), "default", uuid.New())
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListFundingSources(t.Context(), "tenant", uuid.Nil)
	assertErrorIs(t, err, ErrMissingWalletID)
}

func TestValidateFundingSourceMerge(t *testing.T) {
	walletID := uuid.New()
	existing := FundingSource{
		TenantID:          "tenant",
		WalletID:          walletID,
		SourceType:        "card",
		PSPProvider:       sql.NullString{String: "provider-a", Valid: true},
		ExternalReference: sql.NullString{String: "source-ref", Valid: true},
		Currency:          "USD",
		SourceDetails:     []byte(`{"account_last4":"4321","bank":"044"}`),
		WithdrawalMethod:  []byte(`{"account_number":"1234567890","bank_code":"044"}`),
	}
	if err := ValidateFundingSourceMerge(&existing, existing); err != nil {
		t.Fatalf("ValidateFundingSourceMerge() error = %v", err)
	}

	cases := map[string]FundingSource{
		"tenant": {
			TenantID:          "tenant-b",
			WalletID:          walletID,
			SourceType:        existing.SourceType,
			PSPProvider:       existing.PSPProvider,
			ExternalReference: existing.ExternalReference,
			Currency:          existing.Currency,
		},
		"wallet": {
			TenantID:          existing.TenantID,
			WalletID:          uuid.New(),
			SourceType:        existing.SourceType,
			PSPProvider:       existing.PSPProvider,
			ExternalReference: existing.ExternalReference,
			Currency:          existing.Currency,
		},
		"source type": {
			TenantID:          existing.TenantID,
			WalletID:          walletID,
			SourceType:        "bank_account",
			PSPProvider:       existing.PSPProvider,
			ExternalReference: existing.ExternalReference,
			Currency:          existing.Currency,
		},
		"provider": {
			TenantID:          existing.TenantID,
			WalletID:          walletID,
			SourceType:        existing.SourceType,
			PSPProvider:       sql.NullString{String: "provider-b", Valid: true},
			ExternalReference: existing.ExternalReference,
			Currency:          existing.Currency,
		},
		"external reference": {
			TenantID:          existing.TenantID,
			WalletID:          walletID,
			SourceType:        existing.SourceType,
			PSPProvider:       existing.PSPProvider,
			ExternalReference: sql.NullString{String: "other-ref", Valid: true},
			Currency:          existing.Currency,
		},
		"currency": {
			TenantID:          existing.TenantID,
			WalletID:          walletID,
			SourceType:        existing.SourceType,
			PSPProvider:       existing.PSPProvider,
			ExternalReference: existing.ExternalReference,
			Currency:          "EUR",
			SourceDetails:     existing.SourceDetails,
			WithdrawalMethod:  existing.WithdrawalMethod,
		},
		"source details": {
			TenantID:          existing.TenantID,
			WalletID:          walletID,
			SourceType:        existing.SourceType,
			PSPProvider:       existing.PSPProvider,
			ExternalReference: existing.ExternalReference,
			Currency:          existing.Currency,
			SourceDetails:     []byte(`{"account_last4":"9999","bank":"044"}`),
			WithdrawalMethod:  existing.WithdrawalMethod,
		},
		"withdrawal method": {
			TenantID:          existing.TenantID,
			WalletID:          walletID,
			SourceType:        existing.SourceType,
			PSPProvider:       existing.PSPProvider,
			ExternalReference: existing.ExternalReference,
			Currency:          existing.Currency,
			SourceDetails:     existing.SourceDetails,
			WithdrawalMethod:  []byte(`{"account_number":"0000000000","bank_code":"044"}`),
		},
	}
	for name, requested := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateFundingSourceMerge(&existing, requested)
			assertErrorIs(t, err, ErrDuplicateFundingSource)
		})
	}
}

func TestCreateFundingLinkValidation(t *testing.T) {
	s := &Store{}
	link := LedgerFundingLink{
		TenantID:        "tenant",
		LedgerEntryID:   1,
		FundingSourceID: 2,
		Amount:          100,
		Currency:        "USD",
	}

	_, err := s.CreateFundingLink(t.Context(), LedgerFundingLink{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := link
	bad.TenantID = "default"
	_, err = s.CreateFundingLink(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = link
	bad.LedgerEntryID = 0
	_, err = s.CreateFundingLink(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingLedgerEntryID)

	bad = link
	bad.FundingSourceID = 0
	_, err = s.CreateFundingLink(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingFundingSourceID)

	bad = link
	bad.Amount = 0
	_, err = s.CreateFundingLink(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidAmount)

	bad = link
	bad.Currency = ""
	_, err = s.CreateFundingLink(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingCurrency)
}

func TestValidateFundingLinkReplay(t *testing.T) {
	existing := LedgerFundingLink{
		TenantID:        "tenant",
		LedgerEntryID:   1,
		FundingSourceID: 2,
		Amount:          100,
		Currency:        "USD",
	}
	if err := ValidateFundingLinkReplay(&existing, existing); err != nil {
		t.Fatalf("ValidateFundingLinkReplay() error = %v", err)
	}

	cases := map[string]LedgerFundingLink{
		"ledger entry": {
			TenantID:        existing.TenantID,
			LedgerEntryID:   3,
			FundingSourceID: existing.FundingSourceID,
			Amount:          existing.Amount,
			Currency:        existing.Currency,
		},
		"funding source": {
			TenantID:        existing.TenantID,
			LedgerEntryID:   existing.LedgerEntryID,
			FundingSourceID: 4,
			Amount:          existing.Amount,
			Currency:        existing.Currency,
		},
		"amount": {
			TenantID:        existing.TenantID,
			LedgerEntryID:   existing.LedgerEntryID,
			FundingSourceID: existing.FundingSourceID,
			Amount:          101,
			Currency:        existing.Currency,
		},
		"currency": {
			TenantID:        existing.TenantID,
			LedgerEntryID:   existing.LedgerEntryID,
			FundingSourceID: existing.FundingSourceID,
			Amount:          existing.Amount,
			Currency:        "EUR",
		},
	}
	for name, requested := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateFundingLinkReplay(&existing, requested)
			assertErrorIs(t, err, ErrDuplicateFundingLink)
		})
	}
}

func TestValidateFundingLinkLedgerEntry(t *testing.T) {
	walletID := uuid.New()
	entry := LedgerEntry{
		ID:        10,
		TenantID:  "tenant",
		WalletID:  walletID,
		EntryType: "credit",
		Amount:    100,
		Currency:  "USD",
	}
	source := FundingSource{
		ID:       20,
		TenantID: "tenant",
		WalletID: walletID,
		Currency: "USD",
	}
	link := LedgerFundingLink{
		TenantID:        "tenant",
		LedgerEntryID:   10,
		FundingSourceID: 20,
		Amount:          100,
		Currency:        "USD",
	}
	if err := ValidateFundingLinkLedgerEntry(&entry, &source, link); err != nil {
		t.Fatalf("ValidateFundingLinkLedgerEntry() error = %v", err)
	}

	debit := entry
	debit.EntryType = "debit"
	if err := ValidateFundingLinkLedgerEntry(&debit, &source, link); err != nil {
		t.Fatalf("ValidateFundingLinkLedgerEntry() debit error = %v", err)
	}

	amountMismatch := entry
	amountMismatch.Amount = 99
	assertErrorIs(t, ValidateFundingLinkLedgerEntry(&amountMismatch, &source, link), ErrInvalidAmount)

	currencyMismatch := entry
	currencyMismatch.Currency = "EUR"
	assertErrorIs(t, ValidateFundingLinkLedgerEntry(&currencyMismatch, &source, link), ErrCurrencyMismatch)

	sourceCurrencyMismatch := source
	sourceCurrencyMismatch.Currency = "EUR"
	assertErrorIs(t, ValidateFundingLinkLedgerEntry(&entry, &sourceCurrencyMismatch, link), ErrCurrencyMismatch)

	sourceWalletMismatch := source
	sourceWalletMismatch.WalletID = uuid.New()
	assertErrorIs(t, ValidateFundingLinkLedgerEntry(&entry, &sourceWalletMismatch, link), ErrFundingSourceNotFound)

	invalidType := entry
	invalidType.EntryType = "pending"
	assertErrorIs(t, ValidateFundingLinkLedgerEntry(&invalidType, &source, link), ErrInvalidDirection)

	assertErrorIs(t, ValidateFundingLinkLedgerEntry(nil, &source, link), ErrLedgerEntryNotFound)
	assertErrorIs(t, ValidateFundingLinkLedgerEntry(&entry, nil, link), ErrFundingSourceNotFound)
}

func TestValidateWithdrawalDestinationLinkLedgerEntry(t *testing.T) {
	walletID := uuid.New()
	entry := LedgerEntry{
		ID:        10,
		TenantID:  "tenant",
		WalletID:  walletID,
		EntryType: "debit",
		Amount:    1000,
		Currency:  "AED",
	}
	destination := WithdrawalDestination{
		ID:                    20,
		TenantID:              "tenant",
		WalletID:              walletID,
		Currency:              "AED",
		LinkedFundingSourceID: 30,
		IsActive:              true,
	}
	link := LedgerWithdrawalDestinationLink{
		TenantID:      "tenant",
		LedgerEntryID: 10,
		DestinationID: 20,
		Amount:        1000,
		Currency:      "AED",
	}
	if err := ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &destination, link); err != nil {
		t.Fatalf("ValidateWithdrawalDestinationLinkLedgerEntry() error = %v", err)
	}

	credit := entry
	credit.EntryType = "credit"
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&credit, &destination, link), ErrInvalidDirection)

	otherDestination := destination
	otherDestination.Currency = "EUR"
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &otherDestination, link), ErrCurrencyMismatch)

	amountMismatch := link
	amountMismatch.Amount = link.Amount + 1
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &destination, amountMismatch), ErrInvalidAmount)

	currencyMismatch := link
	currencyMismatch.Currency = "EUR"
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &destination, currencyMismatch), ErrCurrencyMismatch)

	walletMismatch := destination
	walletMismatch.WalletID = uuid.New()
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &walletMismatch, link), ErrDestinationNotFound)

	inactive := destination
	inactive.IsActive = false
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &inactive, link), ErrDestinationNotFound)

	unlinked := destination
	unlinked.LinkedFundingSourceID = 0
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, &unlinked, link), ErrDestinationNotVerified)

	otherEntry := entry
	otherEntry.ID = 11
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&otherEntry, &destination, link), ErrDuplicateDestinationLink)

	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(nil, &destination, link), ErrLedgerEntryNotFound)
	assertErrorIs(t, ValidateWithdrawalDestinationLinkLedgerEntry(&entry, nil, link), ErrDestinationNotFound)
}

func TestValidateWithdrawalDestinationLinkReplay(t *testing.T) {
	existing := LedgerWithdrawalDestinationLink{
		TenantID:      "tenant",
		LedgerEntryID: 1,
		DestinationID: 2,
		Amount:        100,
		Currency:      "USD",
	}
	if err := ValidateWithdrawalDestinationLinkReplay(&existing, existing); err != nil {
		t.Fatalf("ValidateWithdrawalDestinationLinkReplay() error = %v", err)
	}

	cases := map[string]LedgerWithdrawalDestinationLink{
		"ledger entry": {
			TenantID:      existing.TenantID,
			LedgerEntryID: 3,
			DestinationID: existing.DestinationID,
			Amount:        existing.Amount,
			Currency:      existing.Currency,
		},
		"destination": {
			TenantID:      existing.TenantID,
			LedgerEntryID: existing.LedgerEntryID,
			DestinationID: 4,
			Amount:        existing.Amount,
			Currency:      existing.Currency,
		},
		"amount": {
			TenantID:      existing.TenantID,
			LedgerEntryID: existing.LedgerEntryID,
			DestinationID: existing.DestinationID,
			Amount:        101,
			Currency:      existing.Currency,
		},
		"currency": {
			TenantID:      existing.TenantID,
			LedgerEntryID: existing.LedgerEntryID,
			DestinationID: existing.DestinationID,
			Amount:        existing.Amount,
			Currency:      "EUR",
		},
	}
	for name, requested := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidateWithdrawalDestinationLinkReplay(&existing, requested)
			assertErrorIs(t, err, ErrDuplicateDestinationLink)
		})
	}
}

func TestGetFundingSourceByPSPRefValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetFundingSourceByPSPRef(t.Context(), "", "coinsbuy", "ref-1")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetFundingSourceByPSPRef(t.Context(), "default", "coinsbuy", "ref-1")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetFundingSourceByPSPRef(t.Context(), "tenant", "", "ref-1")
	assertErrorIs(t, err, ErrMissingProviderCode)

	_, err = s.GetFundingSourceByPSPRef(t.Context(), "tenant", "coinsbuy", "")
	assertErrorIs(t, err, ErrMissingReferenceID)
}

func TestListPendingWithdrawalApprovalsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPendingWithdrawalApprovals(t.Context(), "", 10, 0)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPendingWithdrawalApprovals(t.Context(), "default", 10, 0)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListPendingWithdrawalApprovals(t.Context(), "tenant", 0, 0)
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListPendingWithdrawalApprovals(t.Context(), "tenant", 10, -1)
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestListPSPTransactionsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactions(t.Context(), PSPTransactionFilter{})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "default", Limit: 10})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "tenant", Limit: 0})
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "tenant", Limit: 10, Offset: -1})
	assertErrorIs(t, err, ErrInvalidOffset)

	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "tenant", Status: "complete", Limit: 10})
	assertErrorIs(t, err, ErrInvalidStatus)

	now := time.Now().UTC()
	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "tenant", Limit: 10, Offset: 0, Start: now})
	assertErrorIs(t, err, ErrMissingEndTime)

	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "tenant", Limit: 10, Offset: 0, End: now})
	assertErrorIs(t, err, ErrMissingStartTime)

	_, err = s.ListPSPTransactions(t.Context(), PSPTransactionFilter{TenantID: "tenant", Limit: 10, Offset: 0, Start: now.Add(time.Hour), End: now})
	assertErrorIs(t, err, ErrInvalidTimeRange)
}

func TestListFeeConfigsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListFeeConfigs(t.Context(), FeeConfigFilter{})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListFeeConfigs(t.Context(), FeeConfigFilter{TenantID: "default", Limit: 10})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListFeeConfigs(t.Context(), FeeConfigFilter{TenantID: "tenant", Limit: 0})
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListFeeConfigs(t.Context(), FeeConfigFilter{TenantID: "tenant", Limit: 10, Offset: -1})
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestGetFeeConfigForAmountValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetFeeConfigForAmount(t.Context(), "", "deposit", "USD", 100)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetFeeConfigForAmount(t.Context(), "default", "deposit", "USD", 100)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetFeeConfigForAmount(t.Context(), "tenant", "", "USD", 100)
	assertErrorIs(t, err, ErrMissingTransactionType)

	_, err = s.GetFeeConfigForAmount(t.Context(), "tenant", "deposit", "", 100)
	assertErrorIs(t, err, ErrMissingCurrency)

	_, err = s.GetFeeConfigForAmount(t.Context(), "tenant", "deposit", "USD", 0)
	assertErrorIs(t, err, ErrInvalidAmount)

	_, err = s.GetFeeConfigForAmount(t.Context(), "tenant", "deposit", "USD", -1)
	assertErrorIs(t, err, ErrInvalidAmount)
}

func TestCreateFeeConfigValidation(t *testing.T) {
	s := &Store{}
	cfg := FeeConfig{
		TenantID:            "tenant",
		TransactionType:     "deposit",
		Currency:            "USD",
		TierMin:             0,
		PercentageFee:       decimal.NewFromFloat(1.5),
		FlatFee:             0,
		MinFee:              0,
		IsActive:            true,
		CreatedByOperatorID: 1,
	}

	_, err := s.CreateFeeConfig(t.Context(), FeeConfig{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := cfg
	bad.TenantID = "default"
	_, err = s.CreateFeeConfig(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = cfg
	bad.TransactionType = ""
	_, err = s.CreateFeeConfig(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingTransactionType)

	bad = cfg
	bad.Currency = ""
	_, err = s.CreateFeeConfig(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingCurrency)

	bad = cfg
	bad.TierMin = -1
	_, err = s.CreateFeeConfig(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidAmount)

	bad = cfg
	bad.PercentageFee = decimal.NewFromFloat(-1)
	_, err = s.CreateFeeConfig(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidPercentage)

	bad = cfg
	bad.CreatedByOperatorID = 0
	_, err = s.CreateFeeConfig(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingOperatorID)
}

func TestGetLimitsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetLimits(t.Context(), "", "basic", "withdrawal", "USD")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetLimits(t.Context(), "default", "basic", "withdrawal", "USD")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetLimits(t.Context(), "tenant", "", "withdrawal", "USD")
	assertErrorIs(t, err, ErrMissingKYCTier)

	_, err = s.GetLimits(t.Context(), "tenant", "basic", "", "USD")
	assertErrorIs(t, err, ErrMissingTransactionType)

	_, err = s.GetLimits(t.Context(), "tenant", "basic", "withdrawal", "")
	assertErrorIs(t, err, ErrMissingCurrency)
}

func TestListExchangeRatesValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListExchangeRates(t.Context(), ExchangeRateFilter{})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListExchangeRates(t.Context(), ExchangeRateFilter{TenantID: "default", Limit: 10})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListExchangeRates(t.Context(), ExchangeRateFilter{TenantID: "tenant", Limit: 0})
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListExchangeRates(t.Context(), ExchangeRateFilter{TenantID: "tenant", Limit: 10, Offset: -1})
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestGetActiveRateValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetActiveRate(t.Context(), "", "USD", "EUR")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetActiveRate(t.Context(), "default", "USD", "EUR")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetActiveRate(t.Context(), "tenant", "", "EUR")
	assertErrorIs(t, err, ErrMissingBaseCurrency)

	_, err = s.GetActiveRate(t.Context(), "tenant", "USD", "")
	assertErrorIs(t, err, ErrMissingQuoteCurrency)
}

func TestCreateExchangeRateValidation(t *testing.T) {
	s := &Store{}
	rate := ExchangeRate{
		TenantID:        "tenant",
		BaseCurrency:    "USD",
		QuoteCurrency:   "EUR",
		BuyRate:         decimal.NewFromFloat(1.1),
		SellRate:        decimal.NewFromFloat(1.2),
		SetByOperatorID: 1,
		EffectiveFrom:   time.Now().UTC(),
	}

	_, err := s.CreateExchangeRate(t.Context(), ExchangeRate{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := rate
	bad.TenantID = "default"
	_, err = s.CreateExchangeRate(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = rate
	bad.BaseCurrency = ""
	_, err = s.CreateExchangeRate(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingBaseCurrency)

	bad = rate
	bad.QuoteCurrency = ""
	_, err = s.CreateExchangeRate(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingQuoteCurrency)

	bad = rate
	bad.SetByOperatorID = 0
	_, err = s.CreateExchangeRate(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingOperatorID)

	bad = rate
	bad.EffectiveFrom = time.Time{}
	_, err = s.CreateExchangeRate(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingStartTime)

	bad = rate
	bad.BuyRate = decimal.NewFromInt(0)
	_, err = s.CreateExchangeRate(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidRate)
}

func TestAddPSPTransactionAmountValidation(t *testing.T) {
	s := &Store{}
	base := PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
	}

	cases := []struct {
		name    string
		mutate  func(a *PSPTransactionAmount)
		wantErr error
	}{
		{"missing-tenant", func(a *PSPTransactionAmount) { a.TenantID = "" }, ErrMissingTenantID},
		{"invalid-tenant", func(a *PSPTransactionAmount) { a.TenantID = "default" }, ErrInvalidTenantID},
		{"missing-psp-tx", func(a *PSPTransactionAmount) { a.PSPTransactionID = 0 }, ErrMissingPSPTransactionID},
		{"missing-kind", func(a *PSPTransactionAmount) { a.AmountKind = "" }, ErrMissingAmountKind},
		{"invalid-kind", func(a *PSPTransactionAmount) { a.AmountKind = PSPAmountKind("bogus") }, ErrInvalidAmountKind},
		{"invalid-amount", func(a *PSPTransactionAmount) { a.Amount = 0 }, ErrInvalidAmount},
		{"missing-currency", func(a *PSPTransactionAmount) { a.Currency = "" }, ErrMissingCurrency},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			amount := base
			tc.mutate(&amount)
			_, err := s.AddPSPTransactionAmount(t.Context(), amount)
			assertErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestAddPSPTransactionAmountFXValidation(t *testing.T) {
	s := &Store{}
	amount := PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
	}

	amount.FxRate = decimal.NullDecimal{Valid: true}
	_, err := s.AddPSPTransactionAmount(t.Context(), amount)
	assertErrorIs(t, err, ErrMissingFXCurrency)

	amount = PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
		FxBaseCurrency:   sql.NullString{String: "USD", Valid: true},
		FxQuoteCurrency:  sql.NullString{String: "EUR", Valid: true},
	}
	_, err = s.AddPSPTransactionAmount(t.Context(), amount)
	assertErrorIs(t, err, ErrMissingFXRate)
}

func TestAddPSPTransactionAmountsValidation(t *testing.T) {
	s := &Store{}
	inputs := []PSPTransactionAmountInput{
		{
			AmountKind: PSPAmountReported,
			Amount:     100,
			Currency:   "USD",
		},
	}

	_, err := s.AddPSPTransactionAmounts(t.Context(), "", 1, inputs)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.AddPSPTransactionAmounts(t.Context(), "default", 1, inputs)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.AddPSPTransactionAmounts(t.Context(), "tenant", 0, inputs)
	assertErrorIs(t, err, ErrMissingPSPTransactionID)

	_, err = s.AddPSPTransactionAmounts(t.Context(), "tenant", 1, nil)
	assertErrorIs(t, err, ErrMissingAmounts)

	bad := []PSPTransactionAmountInput{
		{
			AmountKind: PSPAmountKind("bogus"),
			Amount:     100,
			Currency:   "USD",
		},
	}
	_, err = s.AddPSPTransactionAmounts(t.Context(), "tenant", 1, bad)
	assertErrorIs(t, err, ErrInvalidAmountKind)
}

func TestValidatePSPTransactionAmountReplay(t *testing.T) {
	existing := PSPTransactionAmount{
		TenantID:         "tenant",
		PSPTransactionID: 1,
		AmountKind:       PSPAmountReported,
		Amount:           100,
		Currency:         "USD",
		FxRate:           decimal.NullDecimal{Decimal: decimal.RequireFromString("3.75000000"), Valid: true},
		FxBaseCurrency:   sql.NullString{String: "USD", Valid: true},
		FxQuoteCurrency:  sql.NullString{String: "AED", Valid: true},
		FxSource:         sql.NullString{String: "provider", Valid: true},
	}
	if err := ValidatePSPTransactionAmountReplay(&existing, existing); err != nil {
		t.Fatalf("ValidatePSPTransactionAmountReplay() error = %v", err)
	}

	cases := map[string]PSPTransactionAmount{
		"tenant": {
			TenantID:         "other",
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"psp transaction": {
			TenantID:         existing.TenantID,
			PSPTransactionID: 2,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"kind": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       PSPAmountSettlement,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"amount": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           101,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"currency": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         "EUR",
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"fx rate": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           decimal.NullDecimal{Decimal: decimal.RequireFromString("3.76000000"), Valid: true},
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"fx base": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   sql.NullString{String: "EUR", Valid: true},
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         existing.FxSource,
		},
		"fx quote": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  sql.NullString{String: "EUR", Valid: true},
			FxSource:         existing.FxSource,
		},
		"fx source": {
			TenantID:         existing.TenantID,
			PSPTransactionID: existing.PSPTransactionID,
			AmountKind:       existing.AmountKind,
			Amount:           existing.Amount,
			Currency:         existing.Currency,
			FxRate:           existing.FxRate,
			FxBaseCurrency:   existing.FxBaseCurrency,
			FxQuoteCurrency:  existing.FxQuoteCurrency,
			FxSource:         sql.NullString{String: "manual", Valid: true},
		},
	}
	for name, requested := range cases {
		t.Run(name, func(t *testing.T) {
			err := ValidatePSPTransactionAmountReplay(&existing, requested)
			assertErrorIs(t, err, ErrDuplicateAmount)
		})
	}
}

func TestListPSPTransactionAmountsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactionAmounts(t.Context(), "", 1)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionAmounts(t.Context(), "default", 1)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListPSPTransactionAmounts(t.Context(), "tenant", 0)
	assertErrorIs(t, err, ErrMissingPSPTransactionID)
}

func TestListPSPTransactionAmountsByKindValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListPSPTransactionAmountsByKind(t.Context(), "", 1, PSPAmountReported)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "default", 1, PSPAmountReported)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "tenant", 0, PSPAmountReported)
	assertErrorIs(t, err, ErrMissingPSPTransactionID)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "tenant", 1, "")
	assertErrorIs(t, err, ErrMissingAmountKind)

	_, err = s.ListPSPTransactionAmountsByKind(t.Context(), "tenant", 1, PSPAmountKind("bogus"))
	assertErrorIs(t, err, ErrInvalidAmountKind)
}

func TestDeactivateWithdrawalDestinationValidation(t *testing.T) {
	s := &Store{}
	err := s.DeactivateWithdrawalDestination(t.Context(), "", 1, time.Now().UTC())
	assertErrorIs(t, err, ErrMissingTenantID)

	err = s.DeactivateWithdrawalDestination(t.Context(), "default", 1, time.Now().UTC())
	assertErrorIs(t, err, ErrInvalidTenantID)

	err = s.DeactivateWithdrawalDestination(t.Context(), "tenant", 0, time.Now().UTC())
	assertErrorIs(t, err, ErrMissingDestinationID)

	err = s.DeactivateWithdrawalDestination(t.Context(), "tenant", 1, time.Time{})
	assertErrorIs(t, err, ErrMissingUpdatedAt)
}

func TestListManualTransfersByStatusValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListManualTransfersByStatus(t.Context(), "", "pending", 10, 0)
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListManualTransfersByStatus(t.Context(), "default", "pending", 10, 0)
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListManualTransfersByStatus(t.Context(), "tenant", "", 10, 0)
	assertErrorIs(t, err, ErrMissingStatus)

	_, err = s.ListManualTransfersByStatus(t.Context(), "tenant", "bogus", 10, 0)
	assertErrorIs(t, err, ErrInvalidStatus)

	_, err = s.ListManualTransfersByStatus(t.Context(), "tenant", "pending", 0, 0)
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListManualTransfersByStatus(t.Context(), "tenant", "pending", 10, -1)
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestListAuditEventsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListAuditEvents(t.Context(), AuditLogFilter{TenantID: "", Limit: 10})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListAuditEvents(t.Context(), AuditLogFilter{TenantID: "default", Limit: 10})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListAuditEvents(t.Context(), AuditLogFilter{TenantID: "tenant", Limit: 0})
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListAuditEvents(t.Context(), AuditLogFilter{TenantID: "tenant", Limit: 10, Offset: -1})
	assertErrorIs(t, err, ErrInvalidOffset)

	_, err = s.ListAuditEvents(t.Context(), AuditLogFilter{TenantID: "tenant", Limit: 10, Start: time.Now().UTC()})
	assertErrorIs(t, err, ErrMissingEndTime)
}

func TestInsertAuditEventValidation(t *testing.T) {
	s := &Store{}
	event := AuditEvent{
		TenantID:  "tenant",
		EventType: "wallet",
		ActorType: "admin",
		ActorID:   "admin-1",
		Action:    "update",
	}

	err := s.InsertAuditEvent(t.Context(), AuditEvent{})
	assertErrorIs(t, err, ErrMissingTenantID)

	bad := event
	bad.TenantID = "default"
	err = s.InsertAuditEvent(t.Context(), bad)
	assertErrorIs(t, err, ErrInvalidTenantID)

	bad = event
	bad.EventType = ""
	err = s.InsertAuditEvent(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingEventType)

	bad = event
	bad.ActorType = ""
	err = s.InsertAuditEvent(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingActorType)

	bad = event
	bad.ActorID = ""
	err = s.InsertAuditEvent(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingActorID)

	bad = event
	bad.Action = ""
	err = s.InsertAuditEvent(t.Context(), bad)
	assertErrorIs(t, err, ErrMissingAction)
}

func TestOperatorIdentityValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetOperatorIdentity(t.Context(), "", "subject")
	assertErrorIs(t, err, ErrMissingOperatorIssuer)
	_, err = s.GetOperatorIdentity(t.Context(), " https://identity.example ", "subject")
	assertErrorIs(t, err, ErrInvalidOperatorIssuer)
	_, err = s.GetOperatorIdentity(t.Context(), "https://identity.example", "")
	assertErrorIs(t, err, ErrMissingOperatorSubject)
	_, err = s.GetOperatorIdentity(t.Context(), "https://identity.example", " subject ")
	assertErrorIs(t, err, ErrInvalidOperatorSubject)
	_, err = s.GetOperatorIdentityByID(t.Context(), 0)
	assertErrorIs(t, err, ErrMissingOperatorID)
}

func TestGetPSPConfigOverrideValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetPSPConfigOverride(t.Context(), "", "provider", PSPConfigScope{})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetPSPConfigOverride(t.Context(), "default", "provider", PSPConfigScope{})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetPSPConfigOverride(t.Context(), "tenant", "", PSPConfigScope{})
	assertErrorIs(t, err, ErrMissingProviderCode)
}

func TestGetPSPConfigValidation(t *testing.T) {
	s := &Store{}
	_, err := s.GetPSPConfig(t.Context(), "", "provider")
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.GetPSPConfig(t.Context(), "default", "provider")
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.GetPSPConfig(t.Context(), "tenant", "")
	assertErrorIs(t, err, ErrMissingProviderCode)

	_, _, err = s.ResolvePSPConfig(t.Context(), "default", "provider", PSPConfigScope{})
	assertErrorIs(t, err, ErrInvalidTenantID)
}

func TestListAvailablePSPMethodsValidation(t *testing.T) {
	s := &Store{}
	_, err := s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{Direction: "deposit", Limit: 10})
	assertErrorIs(t, err, ErrMissingTenantID)

	_, err = s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{TenantID: "default", Direction: "deposit", Limit: 10})
	assertErrorIs(t, err, ErrInvalidTenantID)

	_, err = s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{TenantID: "tenant", Limit: 10})
	assertErrorIs(t, err, ErrMissingDirection)

	_, err = s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{TenantID: "tenant", Direction: "refund", Limit: 10})
	assertErrorIs(t, err, ErrInvalidDirection)

	_, err = s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{TenantID: "tenant", Direction: "deposit", Amount: -1, Limit: 10})
	assertErrorIs(t, err, ErrInvalidAmount)

	_, err = s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{TenantID: "tenant", Direction: "deposit"})
	assertErrorIs(t, err, ErrInvalidLimit)

	_, err = s.ListAvailablePSPMethods(t.Context(), PSPMethodFilter{TenantID: "tenant", Direction: "deposit", Limit: 10, Offset: -1})
	assertErrorIs(t, err, ErrInvalidOffset)
}

func TestAvailablePSPMethodsFromConfigsPaginatesAfterEligibility(t *testing.T) {
	filter := PSPMethodFilter{
		Currency: "AED",
		Region:   "AE",
		Amount:   500,
		Limit:    1,
		Offset:   1,
	}
	configs := []*PSPConfig{
		{
			TenantID:              "tenant",
			ProviderCode:          "withdraw-only",
			ProviderName:          "A Withdraw",
			IsActive:              true,
			SupportsWithdrawal:    true,
			EnabledCurrencies:     StringArray{"AED"},
			SupportedRegions:      StringArray{"AE"},
			MinAmount:             sql.NullInt64{Int64: 100, Valid: true},
			MaxAmount:             sql.NullInt64{Int64: 1000, Valid: true},
			DepositInputSchema:    RawJSON(`{"kind":"withdraw"}`),
			PresentationSchema:    RawJSON(`{"kind":"withdraw"}`),
			WithdrawalInputSchema: RawJSON(`{"kind":"withdraw"}`),
		},
		{
			TenantID:           "tenant",
			ProviderCode:       "usd-only",
			ProviderName:       "B USD",
			IsActive:           true,
			SupportsDeposit:    true,
			EnabledCurrencies:  StringArray{"USD"},
			SupportedRegions:   StringArray{"AE"},
			MinAmount:          sql.NullInt64{Int64: 100, Valid: true},
			MaxAmount:          sql.NullInt64{Int64: 1000, Valid: true},
			DepositInputSchema: RawJSON(`{"kind":"usd"}`),
			PresentationSchema: RawJSON(`{"kind":"usd"}`),
		},
		{
			TenantID:           "tenant",
			ProviderCode:       "eligible-a",
			ProviderName:       "C Eligible",
			IsActive:           true,
			SupportsDeposit:    true,
			EnabledCurrencies:  StringArray{"AED"},
			SupportedRegions:   StringArray{"AE"},
			MinAmount:          sql.NullInt64{Int64: 100, Valid: true},
			MaxAmount:          sql.NullInt64{Int64: 1000, Valid: true},
			DepositInputSchema: RawJSON(`{"kind":"eligible-a"}`),
			PresentationSchema: RawJSON(`{"kind":"eligible-a"}`),
		},
		{
			TenantID:           "tenant",
			ProviderCode:       "eligible-b",
			ProviderName:       "D Eligible",
			IsActive:           true,
			SupportsDeposit:    true,
			EnabledCurrencies:  StringArray{"AED"},
			SupportedRegions:   StringArray{"AE"},
			MinAmount:          sql.NullInt64{Int64: 100, Valid: true},
			MaxAmount:          sql.NullInt64{Int64: 1000, Valid: true},
			DepositInputSchema: RawJSON(`{"kind":"eligible-b"}`),
			PresentationSchema: RawJSON(`{"kind":"eligible-b"}`),
		},
	}

	methods := availablePSPMethodsFromConfigs(configs, filter, "deposit")
	if len(methods) != 1 {
		t.Fatalf("expected one paginated method, got %d", len(methods))
	}
	if methods[0].ProviderCode != "eligible-b" {
		t.Fatalf("expected second eligible method after pagination, got %q", methods[0].ProviderCode)
	}
}

func TestAvailablePSPMethodsFromConfigsRequiresConfiguredCurrencies(t *testing.T) {
	configs := []*PSPConfig{
		{
			TenantID:         "tenant",
			ProviderCode:     "unconfigured",
			ProviderName:     "Unconfigured",
			IsActive:         true,
			SupportsDeposit:  true,
			SupportedRegions: StringArray{"AE"},
		},
		{
			TenantID:          "tenant",
			ProviderCode:      "configured",
			ProviderName:      "Configured",
			IsActive:          true,
			SupportsDeposit:   true,
			EnabledCurrencies: StringArray{"AED"},
			SupportedRegions:  StringArray{"AE"},
		},
	}

	methods := availablePSPMethodsFromConfigs(configs, PSPMethodFilter{
		Region: "AE",
		Limit:  10,
	}, "deposit")
	if len(methods) != 1 {
		t.Fatalf("expected only configured-currency method, got %d", len(methods))
	}
	if methods[0].ProviderCode != "configured" {
		t.Fatalf("expected configured method, got %q", methods[0].ProviderCode)
	}

	methods = availablePSPMethodsFromConfigs(configs, PSPMethodFilter{
		Currency: "AED",
		Region:   "AE",
		Limit:    10,
	}, "deposit")
	if len(methods) != 1 {
		t.Fatalf("expected only configured AED method, got %d", len(methods))
	}
	if methods[0].ProviderCode != "configured" {
		t.Fatalf("expected configured AED method, got %q", methods[0].ProviderCode)
	}
}

func TestMergePSPConfigOverrideCanActivateScopedMethod(t *testing.T) {
	base := &PSPConfig{
		TenantID:          "tenant",
		ProviderCode:      "scoped",
		ProviderName:      "Scoped Pay",
		IsActive:          false,
		SupportsDeposit:   false,
		EnabledCurrencies: StringArray{"USD"},
		SupportedRegions:  StringArray{"US"},
		MethodType:        "redirect",
	}
	override := &PSPConfigOverride{
		IsActive:           true,
		SupportsDeposit:    true,
		EnabledCurrencies:  StringArray{"AED"},
		SupportedRegions:   StringArray{"AE"},
		MethodType:         sql.NullString{String: "qr", Valid: true},
		DisplayName:        sql.NullString{String: "Scoped QR", Valid: true},
		DepositInputSchema: RawJSON(`{"kind":"qr"}`),
	}

	merged := mergePSPConfigOverride(base, override)
	methods := availablePSPMethodsFromConfigs([]*PSPConfig{merged}, PSPMethodFilter{
		Currency: "AED",
		Region:   "AE",
		Limit:    10,
	}, "deposit")
	if len(methods) != 1 {
		t.Fatalf("expected scoped override method, got %d", len(methods))
	}
	if methods[0].ProviderCode != "scoped" || methods[0].MethodType != "qr" || methods[0].DisplayName != "Scoped QR" {
		t.Fatalf("unexpected scoped method: %+v", methods[0])
	}
}

func assertErrorIs(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("expected %v, got %v", want, err)
	}
}
