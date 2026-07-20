package utils

// MaskPAN returns a masked string of the PAN
func MaskPAN(PAN string) string {
	length := len(PAN)
	if length < 10 {
		return PAN
	}
	PAN = PAN[:6] + "*****" + PAN[length-4:]
	return PAN
}
