package store

import (
	"context"
	"strings"
	"time"

	"github.com/adonese/noebs/ebs_fields"
)

func (s *Store) UpsertTransactionProjection(ctx context.Context, tenantID string, res ebs_fields.EBSResponse) error {
	tenantID, err := ValidateTenantID(tenantID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.UUID) == "" {
		return ErrMissingUUID
	}
	res.MaskPAN()
	payload, err := marshalTransactionPayload(res)
	if err != nil {
		return err
	}
	db, err := s.ensureDB()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	stmt := s.DB.Rebind(`INSERT INTO transactions(
		tenant_id, token_id, uuid, response_code, response_message, response_status, tran_date_time, tran_amount, tran_fee,
		pan, sender_pan, receiver_pan, terminal_id, system_trace_audit_number, approval_code, service_id, merchant_id,
		bill_type, bill_to, bill_info2, payload, created_at, updated_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (tenant_id, uuid) WHERE uuid IS NOT NULL AND btrim(uuid) <> '' DO UPDATE SET
		token_id = EXCLUDED.token_id,
		response_code = EXCLUDED.response_code,
		response_message = EXCLUDED.response_message,
		response_status = EXCLUDED.response_status,
		tran_date_time = EXCLUDED.tran_date_time,
		tran_amount = EXCLUDED.tran_amount,
		tran_fee = EXCLUDED.tran_fee,
		pan = EXCLUDED.pan,
		sender_pan = EXCLUDED.sender_pan,
		receiver_pan = EXCLUDED.receiver_pan,
		terminal_id = EXCLUDED.terminal_id,
		system_trace_audit_number = EXCLUDED.system_trace_audit_number,
		approval_code = EXCLUDED.approval_code,
		service_id = EXCLUDED.service_id,
		merchant_id = EXCLUDED.merchant_id,
		bill_type = EXCLUDED.bill_type,
		bill_to = EXCLUDED.bill_to,
		bill_info2 = EXCLUDED.bill_info2,
		payload = EXCLUDED.payload,
		updated_at = EXCLUDED.updated_at`)
	_, err = db.ExecContext(ctx, stmt,
		tenantID,
		res.TokenID,
		res.UUID,
		res.ResponseCode,
		res.ResponseMessage,
		res.ResponseStatus,
		res.TranDateTime,
		res.TranAmount,
		res.TranFee,
		res.PAN,
		res.SenderPAN,
		res.ReceiverPAN,
		res.TerminalID,
		res.SystemTraceAuditNumber,
		res.ApprovalCode,
		res.ServiceID,
		res.MerchantID,
		res.BillType,
		res.BillTo,
		res.BillInfo2,
		payload,
		now,
		now,
	)
	return err
}
