package dashboard

import (
	"encoding/json"
	"github.com/adonese/noebs/ebs_fields"
	"github.com/gin-contrib/multitemplate"
	"github.com/gin-gonic/gin"
	"github.com/jinzhu/gorm"
	"strings"
	"time"
	"unicode"
)

type Transaction struct {
	gorm.Model
	ebs_fields.GenericEBSResponseFields
}

type PurchaseModel struct {
	gorm.Model
	ebs_fields.PurchaseFields
}
type Env struct {
	Db *gorm.DB
}

func (e *Env) GetTransactionbyID(c *gin.Context) {
	var tran Transaction
	//id := c.Params.ByName("id")
	err := e.Db.Find(&tran).Error
	if err != nil {
		c.AbortWithStatus(404)
	}
	c.JSON(200, gin.H{"result": tran.ID})

	defer e.Db.Close()
}

type MerchantTransactions struct {
	PurchaseAmount         float32 `json:"purchase_amount"`
	AllTransactions        int     `json:"purchases_count"`
	SuccessfulTransactions int     `json:"successful_transactions"`
	FailedTransactions     int     `json:"failed_transactions"`
}

// To allow Redis to use this struct directly in marshaling
func (p *MerchantTransactions) MarshalBinary() ([]byte, error) {
	return json.Marshal(p)

}

// To allow Redis to use this struct directly in marshaling
func (p *MerchantTransactions) UnmarshalBinary(data []byte) error {
	return json.Unmarshal(data, p)
}

func purchaseSum(tran []string) float32 {
	var trans []MerchantTransactions
	var mtran MerchantTransactions
	for _, k := range tran {
		json.Unmarshal([]byte(k), &mtran)
		trans = append(trans, mtran)
	}
	var sum float32
	for _, k := range trans {
		sum += k.PurchaseAmount
	}
	return sum
}

func ToPurchase(f ebs_fields.PurchaseFields) MerchantTransactions {
	amount := f.TranAmount
	var m MerchantTransactions
	m.PurchaseAmount = amount
	return m
}

type SearchModel struct {
	Page       int    `form:"page"`
	TerminalID string `form:"tid" binding:"required"`
}

func pagination(num int, page int) int {
	r := num % page
	if r == 0 {
		return num / page
	}
	return num/page + 1
}

func errorsCounter(t []Transaction) int {
	var errors int
	for _, v := range t {
		if v.ResponseCode != 0 && v.ResponseStatus == "Successful" {
			errors++
		}
	}
	return errors
}

type dashboardStats struct {
	Amount float32
}

type merchantStats struct {
	//created_at, sum(tran_amount) as amount, terminal_id").Group("terminal_id"
	Amount     float32
	TerminalID string
}

func structToSlice(t []Transaction) []string {
	var s []string
	for _, v := range t {
		d, _ := json.Marshal(v)
		s = append(s, string(d))
	}
	return s
}

func TimeFormatter(t time.Time) string {
	return t.Format(time.RFC3339)
}

func GenerateMultiTemplate() multitemplate.Renderer {
	r := multitemplate.NewRenderer()
	r.AddFromFiles("table", "dashboard/template/base.html", "dashboard/template/table.html")
	r.AddFromFiles("index", "dashboard/template/base.html", "dashboard/template/index.html")
	return r
}

type form struct {
	Text      string `form:"vote" binding:"required"`
	Android   string `form:"android"`
	Ios       string `form:"ios"`
	Subscribe bool   `form:"newsletter"`
}

func (f *form) MarshalBinary() ([]byte, error) {
	return json.Marshal(f)
}

type merchantsIssues struct {
	TerminalID string    `json:"terminalId" binding:"required"`
	Latitude   float32   `json:"lat"`
	Longitude  float32   `json:"long"`
	Time       time.Time `json:"time"`
}

func (m *merchantsIssues) MarshalBinary() ([]byte, error) {
	return json.Marshal(m)
}

func sortTable(db *gorm.DB, searchField, search string, sortField, sortCase string, offset, pageSize int) ([]Transaction, int) {

	var tran []Transaction
	var count int

	searchField = mapSearchField(searchField)
	sortField = mapSearchField(sortField)
	log.Printf("the search field and sort fields are: %s, %s", searchField, sortField)

	if searchField != "" || search != ""{
		// where can you search?
		// terminal_id
		// date time range TODO
		// systemTraceAuditNumber
		switch searchField{
		case "created_at":
			db.Table("transactions").Where("id >= ? AND created_at in (?)", offset, search).Count(&count).Limit(pageSize).Order(sortField + " " + sortCase).Find(&tran)
		case "system_trace_audit_number": // exact match
			db.Table("transactions").Where("id >= ? AND system_trace_audit_number = ?", offset, search).Count(&count).Limit(pageSize).Order(sortField +" "+ sortCase).Find(&tran)
		default:
			db.Table("transactions").Where("id >= ? AND terminal_id LIKE ?", offset, "%"+search+"%").Count(&count).Limit(pageSize).Order(sortField+" " + sortCase).Find(&tran)
		}
	}else{
		// we only want to sort, no searching required
		db.Table("transactions").Where("id >= ?", offset).Count(&count).Limit(pageSize).Order(sortField + " " + sortCase).Find(&tran)
	}
	return tran, count

}

func mapSearchField(f string) string{
	/*
	terminalId: terminal_id
	tranDateTime: tran_date_time
	approvalCode: approval_code
	 */
	var result = f
	for i, v := range []rune(f){
		if i == 0 {
			continue
		}
		if unicode.IsUpper(v){
			if !unicode.IsUpper(rune(f[i-1])){
				result = result[:i] + "_" + strings.ToLower(string(v)) + f[i+1:]
				break
			}

		}
	}
	return strings.ToLower(result)

}