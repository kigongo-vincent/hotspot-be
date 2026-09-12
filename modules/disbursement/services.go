package disbursements

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/transactions"
	"gorm.io/gorm"
)

type DisbursementService struct {
	DB *gorm.DB
}

func NewService(db *gorm.DB) *DisbursementService {
	return &DisbursementService{DB: db}
}

func apiSuccess(c *fiber.Ctx, msg string, data any) error {
	return c.JSON(base.APIResponse{Data: data, Message: msg})
}

func toResponse(d Disbursement) DisbursementResponse {
	return DisbursementResponse{
		ID:              d.ID,
		CreatedAt:       d.CreatedAt.Format("02-01-2006 15:04"),
		DestinationType: string(d.DestinationType),
		BankName:        d.BankName,
		PhoneAccount:    d.PhoneAccount,
		Payee:           d.Payee,
		Amount:          d.Amount,
		TransactionID:   d.TransactionID,
		Charges:         d.Charges,
		Reason:          string(d.Reason),
		Status:          string(d.Status),
	}
}

// ---- Charge computation -----------------------------------------------

// chargeFor returns the fee for a disbursement, by destination type and
// amount range. PLACEHOLDER TIERS — these are simple stand-ins matching
// what the mock data implied (flat 900 for mobile money, flat 5000 for
// bank transfer) and are expected to be replaced with real tiers.
//
// Kept as its own function so replacing the tiers later touches exactly
// one place — CreateDisbursement below never computes a charge itself.
func chargeFor(destinationType DestinationType, amount float64) float64 {
	if destinationType == BankTransfer {
		return 5000
	}

	// Mobile Money tiers — placeholder ranges, adjust as needed.
	switch {
	case amount <= 2500:
		return 100
	case amount <= 50000:
		return 500
	case amount <= 500000:
		return 900
	default:
		return 2000
	}
}

// generateTransactionID mirrors the mock data's shape: a long numeric
// string. Not cryptographically meaningful — just an external-looking
// reference number — so math/rand is fine here (unlike voucher codes,
// this isn't a credential).
func generateTransactionID() string {
	return strconv.FormatInt(time.Now().UnixNano()%1_000_00000_0000, 10)
}

// ---- Create -------------------------------------------------------------

// CreateDisbursement validates the request, computes the charge, persists
// the Disbursement row, and appends TWO ledger transactions via
// transactions.CreateTransaction:
//  1. Deduct for the disbursement amount itself (NoteDisbursement)
//  2. Deduct for the charge (NoteTxnCharges)
//
// These are two separate calls (not summed into one) per product
// decision, so the ledger shows the disbursement and its fee as distinct
// line items rather than one opaque combined deduction.
//
// Both the Disbursement row and both transactions are written in a single
// DB transaction: if either ledger entry fails to post, the disbursement
// itself is rolled back rather than left in a state where money appears
// to have moved with no corresponding ledger record.
//
// TODO: Payee is not resolved here — there is no specified lookup
// (by phone/account) yet, so it's left empty. Wire up a real lookup once
// that's specified; until then this diverges from the old mock data,
// which hardcoded "ODONGO TELLY" for every row.
func (s *DisbursementService) CreateDisbursement(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var body DisbursementRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if !body.DestinationType.IsValid() {
		return base.API_ERROR(c, "invalid destination type")
	}
	if body.DestinationType == BankTransfer && strings.TrimSpace(body.BankName) == "" {
		return base.API_ERROR(c, "bank name is required for bank transfers")
	}
	if strings.TrimSpace(body.PhoneAccount) == "" {
		return base.API_ERROR(c, "destination account is required")
	}
	tmpAmount, _ := strconv.Atoi(body.Amount)
	if float64(tmpAmount) <= 0 {
		return base.API_ERROR(c, "amount must be greater than zero")
	}
	if !body.Reason.IsValid() {
		return base.API_ERROR(c, "invalid reason")
	}

	charge := chargeFor(body.DestinationType, float64(tmpAmount))
	transactionID := generateTransactionID()

	var disbursement Disbursement
	err := s.DB.Transaction(func(dbtx *gorm.DB) error {
		disbursement = Disbursement{
			UserID:          userID,
			DestinationType: body.DestinationType,
			BankName:        body.BankName,
			PhoneAccount:    body.PhoneAccount,
			Payee:           "", // see TODO above
			Amount:          float64(tmpAmount),
			TransactionID:   transactionID,
			Charges:         charge,
			Reason:          body.Reason,
			Status:          Pending,
		}
		if err := dbtx.Create(&disbursement).Error; err != nil {
			return err
		}

		if _, err := transactions.CreateTransaction(
			dbtx, userID, transactions.Deduct, float64(tmpAmount),
			transactionID, transactionID, transactions.NoteDisbursement,
		); err != nil {
			return fmt.Errorf("failed to post disbursement transaction: %w", err)
		}

		if _, err := transactions.CreateTransaction(
			dbtx, userID, transactions.Deduct, charge,
			transactionID, transactionID, transactions.NoteTxnCharges,
		); err != nil {
			return fmt.Errorf("failed to post charge transaction: %w", err)
		}

		return nil
	})
	if err != nil {
		return base.API_ERROR(c, "failed to create disbursement: "+err.Error())
	}

	return apiSuccess(c, "disbursement created", toResponse(disbursement))
}

// ---- Read-only handlers -------------------------------------------------

func (s *DisbursementService) FindAll(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var rows []Disbursement
	if err := s.DB.Where("user_id = ?", userID).Order("id desc").Find(&rows).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch disbursements")
	}

	resp := make([]DisbursementResponse, 0, len(rows))
	for _, d := range rows {
		resp = append(resp, toResponse(d))
	}

	return apiSuccess(c, "disbursements fetched", DisbursementListResponse{Disbursements: resp})
}

func (s *DisbursementService) FindByID(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var d Disbursement
	if err := s.DB.Where("user_id = ?", userID).First(&d, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "disbursement not found")
	}

	return apiSuccess(c, "disbursement fetched", toResponse(d))
}

var searchColumns = []string{"payee", "phone_account", "transaction_id", "bank_name", "reason"}

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

// ListDisbursements handles POST /disbursements/list. Body:
// base.APIRequest. Scoped to the authenticated user. Tabs on the frontend
// (e.g. "Mobile Money", "Bank Transfer", by status) map to a FilterColumn
// on `destination_type` or `status`.
func (s *DisbursementService) ListDisbursements(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var req base.APIRequest
	if err := c.BodyParser(&req); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	req.Pagination = defaultPagination(req.Pagination)

	query := s.DB.Model(&Disbursement{}).Where("user_id = ?", userID)

	for _, f := range req.Columns {
		query = applyFilterColumn(query, f)
	}
	query = applySearch(query, req.Search)

	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return base.API_ERROR(c, "failed to count disbursements: "+err.Error())
	}

	var rows []Disbursement
	offset := int((req.Pagination.Page - 1) * req.Pagination.Limit)
	if err := query.
		Order("id desc").
		Limit(int(req.Pagination.Limit)).
		Offset(offset).
		Find(&rows).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch disbursements: "+err.Error())
	}

	resp := make([]DisbursementResponse, 0, len(rows))
	for _, d := range rows {
		resp = append(resp, toResponse(d))
	}

	return c.JSON(base.APIResponse{
		Data:    DisbursementListResponse{Disbursements: resp},
		Message: "disbursements fetched",
		Pagination: base.PaginationControls{
			Limit: req.Pagination.Limit,
			Page:  req.Pagination.Page,
			Total: uint(total),
		},
	})
}

// GetStats handles GET /disbursements/stats: total disbursed volume for
// each of the last 7 calendar days (including today), oldest first,
// zero-filled for days with no activity. Scoped to the authenticated user.
func (s *DisbursementService) GetStats(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	windowStart := startOfToday.AddDate(0, 0, -6)

	var rows []Disbursement
	if err := s.DB.
		Where("user_id = ? AND created_at >= ?", userID, windowStart).
		Find(&rows).Error; err != nil {
		return base.API_ERROR(c, "failed to load disbursement stats: "+err.Error())
	}

	volumeByDay := make(map[string]float64, 7)
	for _, d := range rows {
		key := d.CreatedAt.Format("2006-01-02")
		volumeByDay[key] += d.Amount
	}

	points := make([]DisbursementDayTotal, 0, 7)
	for i := 0; i < 7; i++ {
		day := windowStart.AddDate(0, 0, i)
		key := day.Format("2006-01-02")
		points = append(points, DisbursementDayTotal{
			Day:    day.Format("Mon"),
			Date:   key,
			Volume: volumeByDay[key],
		})
	}

	return apiSuccess(c, "disbursement stats fetched", DisbursementStatsResponse{Stats: points})
}
