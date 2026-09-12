package transactions

import "gorm.io/gorm"

// Operation is a closed set — a transaction either adds to the ledger
// balance (Topup) or removes from it (Deduct). Kept as a typed string
// (not free-form) so a bad value can be rejected at the DB/validation
// layer rather than silently corrupting the running balance.
type Operation string

const (
	Topup  Operation = "topup"
	Deduct Operation = "deduct"
)

func (o Operation) IsValid() bool {
	return o == Topup || o == Deduct
}

// TransactionNote is a closed set of the reasons a ledger entry can be
// created — matches the note values every internal caller (Vouchers,
// Disbursements, Sales/collections) actually produces today. Kept as a
// typed string (not free-form), same rationale as Operation: an
// unexpected note is a bug in the caller, not a value we want silently
// accepted and then shown as an opaque string in the UI.
type TransactionNote string

const (
	NoteVoucherTxnCharges TransactionNote = "voucher txn charges"
	NoteTxnCharges        TransactionNote = "txn charges"
	NoteCollection        TransactionNote = "collection"
	NoteDisbursement      TransactionNote = "disbursement"
)

func (n TransactionNote) IsValid() bool {
	switch n {
	case NoteVoucherTxnCharges, NoteTxnCharges, NoteCollection, NoteDisbursement:
		return true
	default:
		return false
	}
}

// Transaction is an append-only ledger entry. There is deliberately no
// public Create/Update/Delete route for this module (see router.go) —
// entries are only ever appended internally via CreateTransaction, so the
// running Balance can be trusted to have been computed server-side
// (previous balance ± amount) rather than supplied by a caller.
type Transaction struct {
	gorm.Model
	UserID        uint            `json:"-"`
	TransactionID string          `json:"transactionId"`
	RequestID     string          `json:"requestId"`
	Operation     Operation       `json:"operation"`
	Note          TransactionNote `json:"note"`
	Amount        float64         `json:"amount"`
	Balance       float64         `json:"balance"`
}

// TransactionResponse is the wire-format shape returned to clients.
// Currently identical field-for-field to Transaction minus UserID, but
// kept as its own type (rather than returning Transaction directly) so
// the two are free to diverge later without touching every handler.
type TransactionResponse struct {
	ID            uint    `json:"ID"`
	CreatedAt     string  `json:"CreatedAt"`
	TransactionID string  `json:"transactionId"`
	RequestID     string  `json:"requestId"`
	Operation     string  `json:"operation"`
	Note          string  `json:"note"`
	Amount        float64 `json:"amount"`
	Balance       float64 `json:"balance"`
}

type TransactionListResponse struct {
	Transactions []TransactionResponse `json:"transactions"`
}

// TransactionDayTotal is one point on a volume/activity chart: net
// inflow/outflow for a single calendar day.
type TransactionDayTotal struct {
	Day    string  `json:"day"`
	Date   string  `json:"date"`
	Topup  float64 `json:"topup"`
	Deduct float64 `json:"deduct"`
}

type TransactionStatsResponse struct {
	Stats []TransactionDayTotal `json:"stats"`
}
