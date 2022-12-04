package ebs_fields

type data struct {}
func (d data) TableName() string {
	return "cache_cards"
}
