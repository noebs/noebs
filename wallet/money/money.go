package money

import "github.com/shopspring/decimal"

type Money struct {
	Amount   int64
	Currency string
}

func (m Money) ToMajor() decimal.Decimal {
	scale := ScaleFor(m.Currency)
	return decimal.New(m.Amount, -scale)
}

func FromMajor(amount decimal.Decimal, currency string) Money {
	scale := ScaleFor(currency)
	quantized := amount.Mul(decimal.New(1, scale)).Round(0)
	return Money{
		Amount:   quantized.IntPart(),
		Currency: currency,
	}
}

func ScaleFor(currency string) int32 {
	if scale, ok := currencyScale[currency]; ok {
		return scale
	}
	return 2
}

var currencyScale = map[string]int32{
	"USD": 2,
	"EUR": 2,
	"JPY": 0,
	"KES": 2,
	"GBP": 2,
}
