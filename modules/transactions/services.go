package transactions

import (
	"fmt"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"gorm.io/gorm"
)

type TransactionService struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *TransactionService {
	return &TransactionService{DB: db}
}

func apiSuccess(c *fiber.Ctx, msg string, data any) error {
	return c.JSON(base.APIResponse{Data: data, Message: msg})
}

func toResponse(t Transaction) TransactionResponse {
	return TransactionResponse{
		ID:            t.ID,
		CreatedAt:     t.CreatedAt.Format("02 Jan 2006 15:04"),
		TransactionID: t.TransactionID,
		RequestID:     t.RequestID,
		Operation:     string(t.Operation),
		Note:          string(t.Note),
		Amount:        t.Amount,
		Balance:       t.Balance,
	}
}

// ---- Internal (non-route) API for other modules ---------------------

// CreateTransaction appends a new ledger entry for userID. The resulting
// Balance is always computed server-side as
// (most recent existing balance for this user) ± amount — never trusted
// from a caller — so the ledger can't be corrupted by a caller passing an
// arbitrary balance. A user with no prior transactions starts from a
// balance of 0.
//
// This is the ONLY way a Transaction row should ever be created — there
// is no public HTTP route for it (see router.go), and other modules
// (Vouchers, Sales, ...) should call this directly rather than writing to
// the transactions table themselves.
func CreateTransaction(db *gorm.DB, userID uint, operation Operation, amount float64, transactionID, requestID string, note TransactionNote) (*Transaction, error) {
	if !operation.IsValid() {
		return nil, fmt.Errorf("invalid transaction operation: %q", operation)
	}
	if !note.IsValid() {
		return nil, fmt.Errorf("invalid transaction note: %q", note)
	}
	if amount < 0 {
		return nil, fmt.Errorf("transaction amount must be non-negative")
	}

	var tx Transaction
	err := db.Transaction(func(dbtx *gorm.DB) error {
		var last Transaction
		var runningBalance float64

		err := dbtx.Where("user_id = ?", userID).Order("id desc").First(&last).Error
		switch {
		case err == nil:
			runningBalance = last.Balance
		case err == gorm.ErrRecordNotFound:
			runningBalance = 0
		default:
			return err
		}

		newBalance := runningBalance
		if operation == Topup {
			newBalance += amount
		} else {
			newBalance -= amount
		}

		tx = Transaction{
			UserID:        userID,
			TransactionID: transactionID,
			RequestID:     requestID,
			Operation:     operation,
			Note:          note,
			Amount:        amount,
			Balance:       newBalance,
		}
		return dbtx.Create(&tx).Error
	})
	if err != nil {
		return nil, err
	}

	return &tx, nil
}

// ---- Read-only HTTP handlers -----------------------------------------

// FindAll returns every transaction for the authenticated user, most
// recent first. Kept for parity with other modules' FindAll, though
// ListTransactions (paginated/filterable) is what the admin UI actually
// uses.
func (s *TransactionService) FindAll(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var txs []Transaction
	if err := s.DB.Where("user_id = ?", userID).Order("id desc").Find(&txs).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch transactions")
	}

	resp := make([]TransactionResponse, 0, len(txs))
	for _, t := range txs {
		resp = append(resp, toResponse(t))
	}

	return apiSuccess(c, "transactions fetched", TransactionListResponse{Transactions: resp})
}

// FindByID returns a single transaction, scoped to the authenticated user.
func (s *TransactionService) FindByID(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var tx Transaction
	if err := s.DB.Where("user_id = ?", userID).First(&tx, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "transaction not found")
	}

	return apiSuccess(c, "transaction fetched", toResponse(tx))
}

// searchColumns lists which Transaction columns free-text Search matches.
var searchColumns = []string{"note", "request_id", "transaction_id"}

// applyFilterColumn applies a single base.FilterColumn to the query.
// Operators supported: "=", "!=", "is_null", "not_null", "in" — same
// vocabulary as Vouchers' ListVouchers, kept consistent across modules.
func applyFilterColumn(q *gorm.DB, f base.FilterColumn) *gorm.DB {
	switch f.Operator {
	case "=":
		return q.Where(fmt.Sprintf("%s = ?", f.Column), f.Value)
	case "!=":
		return q.Where(fmt.Sprintf("%s != ?", f.Column), f.Value)
	case "is_null":
		return q.Where(fmt.Sprintf("%s IS NULL", f.Column))
	case "not_null":
		return q.Where(fmt.Sprintf("%s IS NOT NULL", f.Column))
	case "in":
		return q.Where(fmt.Sprintf("%s IN ?", f.Column), f.Value)
	default:
		return q
	}
}

func applySearch(q *gorm.DB, search string) *gorm.DB {
	search = strings.TrimSpace(search)
	if search == "" {
		return q
	}

	like := "%" + search + "%"
	parts := make([]string, len(searchColumns))
	args := make([]interface{}, len(searchColumns))
	for i, col := range searchColumns {
		parts[i] = fmt.Sprintf("%s LIKE ?", col)
		args[i] = like
	}

	return q.Where(strings.Join(parts, " OR "), args...)
}

func defaultPagination(p base.PaginationControls) base.PaginationControls {
	if p.Limit == 0 {
		p.Limit = 10
	}
	if p.Page == 0 {
		p.Page = 1
	}
	return p
}

// ListTransactions handles POST /transactions/list. Body: base.APIRequest
// (Pagination + Columns + Search). Scoped to the authenticated user.
// Tabs on the frontend (e.g. "Topups", "Deductions") map to a
// FilterColumn on `operation` — there is no "Trash" tab here since
// transactions have no soft-delete (they're an append-only ledger).
func (s *TransactionService) ListTransactions(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var req base.APIRequest
	if err := c.BodyParser(&req); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	req.Pagination = defaultPagination(req.Pagination)

	query := s.DB.Model(&Transaction{}).Where("user_id = ?", userID)

	for _, f := range req.Columns {
		query = applyFilterColumn(query, f)
	}
	query = applySearch(query, req.Search)

	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return base.API_ERROR(c, "failed to count transactions: "+err.Error())
	}

	var txs []Transaction
	offset := int((req.Pagination.Page - 1) * req.Pagination.Limit)
	if err := query.
		Order("id desc").
		Limit(int(req.Pagination.Limit)).
		Offset(offset).
		Find(&txs).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch transactions: "+err.Error())
	}

	resp := make([]TransactionResponse, 0, len(txs))
	for _, t := range txs {
		resp = append(resp, toResponse(t))
	}

	return c.JSON(base.APIResponse{
		Data:    TransactionListResponse{Transactions: resp},
		Message: "transactions fetched",
		Pagination: base.PaginationControls{
			Limit: req.Pagination.Limit,
			Page:  req.Pagination.Page,
			Total: uint(total),
		},
	})
}

// GetStats handles GET /transactions/stats: net topup/deduct totals for
// each of the last 7 calendar days (including today), oldest first,
// zero-filled for days with no activity. Scoped to the authenticated user.
func (s *TransactionService) GetStats(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	windowStart := startOfToday.AddDate(0, 0, -6)

	var txs []Transaction
	if err := s.DB.
		Where("user_id = ? AND created_at >= ?", userID, windowStart).
		Find(&txs).Error; err != nil {
		return base.API_ERROR(c, "failed to load transaction stats: "+err.Error())
	}

	type totals struct{ topup, deduct float64 }
	byDay := make(map[string]totals, 7)
	for _, t := range txs {
		key := t.CreatedAt.Format("2006-01-02")
		cur := byDay[key]
		if t.Operation == Topup {
			cur.topup += t.Amount
		} else {
			cur.deduct += t.Amount
		}
		byDay[key] = cur
	}

	points := make([]TransactionDayTotal, 0, 7)
	for i := 0; i < 7; i++ {
		day := windowStart.AddDate(0, 0, i)
		key := day.Format("2006-01-02")
		t := byDay[key]
		points = append(points, TransactionDayTotal{
			Day:    day.Format("Mon"),
			Date:   key,
			Topup:  t.topup,
			Deduct: t.deduct,
		})
	}

	return apiSuccess(c, "transaction stats fetched", TransactionStatsResponse{Stats: points})
}
