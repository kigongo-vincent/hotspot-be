package disbursements

import "gorm.io/gorm"

type DestinationType string

const (
	MobileMoney  DestinationType = "Mobile Money"
	BankTransfer DestinationType = "Bank Transfer"
)

func (d DestinationType) IsValid() bool {
	return d == MobileMoney || d == BankTransfer
}

type Status string

const (
	Success Status = "success"
	Pending Status = "pending"
	Failed  Status = "failed"
)

// Reason mirrors the frontend's REASON_OPTIONS dropdown (forms/disbursement.tsx).
type Reason string

const (
	ReasonPayment Reason = "Payment"
	ReasonRefund  Reason = "Refund"
	ReasonBonus   Reason = "Bonus"
	ReasonSalary  Reason = "Salary"
)

func (r Reason) IsValid() bool {
	switch r {
	case ReasonPayment, ReasonRefund, ReasonBonus, ReasonSalary:
		return true
	default:
		return false
	}
}

// Disbursement is a single outbound payment (mobile money or bank
// transfer). Amount and Charges are always in the base currency unit
// (UGX, matching the frontend's formatUGX — no decimal/cents handling).
//
// Charges is computed server-side from Amount + DestinationType at create
// time (see chargeFor in services.go) — never trusted from the client —
// and is persisted here purely as a display/audit convenience; the actual
// ledger effect of the charge lives in the transactions module (see
// CreateDisbursement).
type Disbursement struct {
	gorm.Model
	UserID          uint            `json:"-"`
	DestinationType DestinationType `json:"destinationType"`
	BankName        string          `json:"bankName"`
	PhoneAccount    string          `json:"phoneAccount"` // always stored E.164 (+256...); see forms/disbursement.tsx for local-format display
	Payee           string          `json:"payee"`
	Amount          float64         `json:"amount"`
	TransactionID   string          `json:"transactionId"`
	Charges         float64         `json:"charges"`
	Reason          Reason          `json:"reason"`
	Status          Status          `json:"status"`
}

type DisbursementResponse struct {
	ID              uint    `json:"ID"`
	CreatedAt       string  `json:"CreatedAt"`
	DestinationType string  `json:"destinationType"`
	BankName        string  `json:"bankName"`
	PhoneAccount    string  `json:"phoneAccount"`
	Payee           string  `json:"payee"`
	Amount          float64 `json:"amount"`
	TransactionID   string  `json:"transactionId"`
	Charges         float64 `json:"charges"`
	Reason          string  `json:"reason"`
	Status          string  `json:"status"`
}

type DisbursementListResponse struct {
	Disbursements []DisbursementResponse `json:"disbursements"`
}

// DisbursementRequest is the client-supplied create payload. Notably
// absent: Payee (resolved/looked up server-side — mock data always used
// "ODONGO TELLY", meaning payee resolution isn't specified yet; see the
// TODO in services.go), TransactionID (generated server-side), Charges
// (computed server-side), and Status (always starts Pending).
type DisbursementRequest struct {
	DestinationType DestinationType `json:"destinationType"`
	BankName        string          `json:"bankName"`
	PhoneAccount    string          `json:"phoneAccount"`
	Amount          string          `json:"amount"`
	Reason          Reason          `json:"reason"`
}

type DisbursementDayTotal struct {
	Day    string  `json:"day"`
	Date   string  `json:"date"`
	Volume float64 `json:"volume"`
}

type DisbursementStatsResponse struct {
	Stats []DisbursementDayTotal `json:"stats"`
}
