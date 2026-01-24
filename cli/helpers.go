package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"

	"github.com/adonese/noebs/ebs_fields"
	"github.com/gofiber/fiber/v2"
)

type redisPurchaseFields map[string]interface{}

func structFields(s interface{}) (fields []map[string]interface{}) {
	val := reflect.ValueOf(s)
	if !val.IsValid() {
		return fields
	}
	for val.Kind() == reflect.Ptr || val.Kind() == reflect.Interface {
		if val.IsNil() {
			return fields
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		return fields
	}

	t := val.Type()

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name := f.Tag.Get("json")
		tag := f.Tag.Get("binding")
		sType := f.Type

		if tag == "" {
			tag = "optional"
		}
		field := map[string]interface{}{
			"field":       name,
			"is_required": tag,
			"type":        sType,
		}
		fields = append(fields, field)

	}
	return fields
}

// endpointToFields the corresponding struct field for the endpoint string.
// we use simple string matching to capture the results
func endpointToFields(e string) interface{} {
	if strings.Contains(e, "cashIn") {
		return ebs_fields.CashInFields{}
	}
	if strings.Contains(e, "cashOut") {
		return ebs_fields.CashOutFields{}
	}
	if strings.Contains(e, "balance") {
		return ebs_fields.BalanceFields{}
	}
	if strings.Contains(e, "billPayment") {
		return ebs_fields.BillPaymentFields{}
	}
	if strings.Contains(e, "cardTransfer") {
		return ebs_fields.CardTransferFields{}
	}
	if strings.Contains(e, "changePin") {
		return ebs_fields.ChangePINFields{}
	}
	if strings.Contains(e, "purchase") {
		return ebs_fields.PurchaseFields{}
	}
	return ebs_fields.EBSResponse{}
}

// generateDoc generates API doc for this particular field
func generateDoc(e string) []map[string]interface{} {

	fields := endpointToFields(e)
	scheme := structFields(&fields)

	return scheme
}

// getAllRoutes gets all routes for this particular engine
// perhaps, it could better be rewritten to explicitly show that
func getAllRoutes() []map[string]string {
	e := GetMainEngine()
	var allRoutes []map[string]string
	for _, stack := range e.Stack() {
		for _, r := range stack {
			name := strings.TrimPrefix(r.Path, "/")
			mapping := map[string]string{
				"http_method": r.Method,
				"path":        name,
			}
			allRoutes = append(allRoutes, mapping)
		}
	}
	return allRoutes
}

var response = ebs_fields.EBSResponse{
	ResponseMessage: "Successful",
	ResponseStatus:  "Successful",
	ResponseCode:    0,
	ReferenceNumber: "094930",
	ApprovalCode:    "0032",
	TranDateTime:    "190613085100",

	TerminalID:             "19000019",
	SystemTraceAuditNumber: 0,
	ClientID:               "ACTS",
	PAN:                    "92220817",
}

func MockEBSServer() *httptest.Server {
	f := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Header().Set("Content-Type", "application/json")

		// write the Generic ResponseBody onto the response writer
		b, err := json.Marshal(&response)
		if err != nil {
			logrusLogger.Errorf("theres an error")
		}
		w.Write(b)

	}
	return httptest.NewServer(http.HandlerFunc(f))
}

func wrapHandler(h interface{}) fiber.Handler {
	switch v := h.(type) {
	case func(*fiber.Ctx) error:
		return v
	case func(*fiber.Ctx):
		return func(c *fiber.Ctx) error {
			v(c)
			return nil
		}
	default:
		return func(c *fiber.Ctx) error {
			return c.Status(http.StatusInternalServerError).JSON(fiber.Map{"code": "invalid_handler", "message": "unsupported handler type"})
		}
	}
}

func additionalFieldsToHash(a string) (map[string]string, error) {
	fields := strings.Split(a, ";")
	if len(fields) < 2 {
		return nil, errors.New("index out of range")
	}
	out := make(map[string]string)
	for _, v := range fields {
		if v == "" {
			continue
		}
		f := strings.SplitN(v, "=", 2)
		if len(f) != 2 {
			return nil, errors.New("invalid key=value pair")
		}
		out[f[0]] = f[1]
	}
	return out, nil
}

type test struct {
	AdditionalData string
	PayeeID        string
}

type mtnBill struct {
	PaidAmount    float64 `json:"PaidAmount"`
	SubNewBalance float64 `json:"SubNewBalance"`
}

func (m *mtnBill) MarshalBinary() (data []byte, err error) {
	d, err := json.Marshal(m)
	return d, err
}

func (m *mtnBill) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, m)
}

func (m *mtnBill) NewFromMap(f map[string]string) {
	m.PaidAmount, _ = strconv.ParseFloat(f["PaidAmount"], 32)
	m.SubNewBalance, _ = strconv.ParseFloat(f["SubNewBalance"], 32)
}

type sudaniBill struct {
	Status string `json:"Status"`
}

func (s *sudaniBill) MarshalBinary() (data []byte, err error) {
	d, err := json.Marshal(s)
	return d, err
}

func (s *sudaniBill) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, s)
}
func (s *sudaniBill) NewFromMap(f map[string]string) {
	s.Status = f["Status"]
}

func generateFields() *ebs_fields.EBSResponse {
	f := &ebs_fields.EBSResponse{}
	f.AdditionalData = "SalesAmount=10.3;FixedFee=22.3;Token=23232;MeterNumber=12345;CustomerName=mohamed"
	f.PayeeID = "0010020001"
	return f
}
