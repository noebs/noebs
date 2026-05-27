package consumer

import (
	"os"
	"strings"
	"testing"
)

func TestLegacyMobileBasedCardVaultHelpersStayRemoved(t *testing.T) {
	rejected := map[string][]string{
		"payment_tokens_service.go": {
			"func (s *Service) GeneratePaymentToken(",
			"func (s *Service) GetPaymentToken(",
			"GetCardsOrFail",
		},
		"user_misc_service.go": {
			"func (s *Service) SetMainCard(",
		},
		"user_service.go": {
			"func (s *Service) GetCards(",
			"func (s *Service) AddCards(",
			"func (s *Service) EditCard(",
			"func (s *Service) RemoveCard(",
			"func (s *Service) CardFromNumber(",
			"func (s *Service) GetUserCards(",
		},
		"handler/routes.go": {
			`router.Get("/users/cards"`,
			`router.Get("/mobile2pan"`,
		},
		"handler/user.go": {
			"func (h *Handler) CardFromNumber(",
			"func (h *Handler) CardsByMobile(",
		},
	}
	for path, tokens := range rejected {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		source := string(data)
		for _, token := range tokens {
			if strings.Contains(source, token) {
				t.Fatalf("%s contains %q; card-vault paths must use gateway user IDs or card-vault owned mobile mappings", path, token)
			}
		}
	}
}
