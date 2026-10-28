package payments

import "gorm.io/gorm"

// PaymentService wraps the dependencies the payment module needs. It has no
// persisted model of its own today — gateway credentials are read from the
// settings module and transactions are recorded through the transactions
// ledger — but the struct keeps the same shape as every other module so it
// can grow without breaking the router wiring.
type PaymentService struct {
	DB *gorm.DB
}

// ChargeRequest mirrors the frontend's InitiatePaymentParams. The phone is
// expected in E.164 form (the UI normalises it before calling).
type ChargeRequest struct {
	Phone          string  `json:"phone"`
	Amount         float64 `json:"amount"`
	Currency       string  `json:"currency"`
	Email          string  `json:"email"`
	Name           string  `json:"name"`
	Reference      string  `json:"reference"`
	Description    string  `json:"description"`
	PaymentMethod  string  `json:"paymentMethod"`
	Method         string  `json:"method"`
	Type           string  `json:"type"`
	RedirectURL    string  `json:"redirect_url"`
	RedirectUrl    string  `json:"redirectUrl"`
}

// FWChargeMetaAuthorization is the authorisation block Flutterwave returns
// for a mobile money charge. `mode` is typically "pin" (the customer
// approves on their phone) or "redirect".
type FWChargeMetaAuthorization struct {
	Mode              string `json:"mode,omitempty"`
	Redirect          string `json:"redirect,omitempty"`
	TransferReference string `json:"transfer_reference,omitempty"`
	TransferAccount   string `json:"transfer_account,omitempty"`
	TransferBank      string `json:"transfer_bank,omitempty"`
	AccountExpiration string `json:"account_expiration,omitempty"`
	TransferNote      string `json:"transfer_note,omitempty"`
	TransferAmount    string `json:"transfer_amount,omitempty"`
}

type FWChargeMeta struct {
	Authorization *FWChargeMetaAuthorization `json:"authorization,omitempty"`
}

// FWChargeData is the subset of Flutterwave's charge response the frontend
// actually consumes.
type FWChargeData struct {
	ID            int    `json:"id,omitempty"`
	FlwRef        string `json:"flw_ref,omitempty"`
	TxRef         string `json:"tx_ref,omitempty"`
	AccountNumber string `json:"account_number,omitempty"`
	BankName      string `json:"bank_name,omitempty"`
	Note          string `json:"note,omitempty"`
	ExpiryDate    string `json:"expiry_date,omitempty"`
}

// FWChargeResponse is shaped to match the frontend's FWInitResponse so the
// payment modal can read res.data.status / res.data.message / res.data.meta
// without any client-side changes.
type FWChargeResponse struct {
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Meta    *FWChargeMeta `json:"meta,omitempty"`
	Data    *FWChargeData `json:"data,omitempty"`
}

// FWVerifyData mirrors Flutterwave's transaction verification payload.
type FWVerifyData struct {
	Status   string  `json:"status"`
	ID       int     `json:"id,omitempty"`
	FlwRef   string  `json:"flw_ref,omitempty"`
	TxRef    string  `json:"tx_ref,omitempty"`
	Amount   float64 `json:"amount,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

// FWVerifyResponse mirrors the frontend's FWVerifyResponse.
type FWVerifyResponse struct {
	Status  string        `json:"status"`
	Message string        `json:"message"`
	Data    *FWVerifyData `json:"data,omitempty"`
}
