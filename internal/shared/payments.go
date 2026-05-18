package shared

import "sync"

var (
	ConfirmedPayments = make(map[string]bool)
	PaymentMutex      sync.RWMutex
)

func ConfirmPayment(code string) {
	PaymentMutex.Lock()
	defer PaymentMutex.Unlock()
	ConfirmedPayments[code] = true
}

func IsPaymentConfirmed(code string) bool {
	PaymentMutex.RLock()
	defer PaymentMutex.RUnlock()
	confirmed, exists := ConfirmedPayments[code]
	return exists && confirmed
}

func ClearPayment(code string) {
	PaymentMutex.Lock()
	defer PaymentMutex.Unlock()
	delete(ConfirmedPayments, code)
}
