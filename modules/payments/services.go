package payments

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/settings"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"github.com/kigongo-vincent/hotspot-be/modules/transactions"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const flutterwaveBaseURL = "https://api.flutterwave.com/v3"

// NewService builds the payments service.
func NewService(db *gorm.DB) *PaymentService {
	return &PaymentService{DB: db}
}

// settingValue reads a single tenant-scoped setting, returning "" when it is
// absent (so callers can fall back to environment variables).
func (s *PaymentService) settingValue(userID uint, key string) string {
	var st settings.Setting
	if err := s.DB.Where("key = ? AND user_id = ?", key, userID).First(&st).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(st.Value)
}

// secretKey resolves the Flutterwave secret key for the calling tenant,
// preferring the value stored through the settings UI and falling back to
// the FLUTTERWAVE_SECRET_KEY environment variable for local/dev setups.
func (s *PaymentService) secretKey(userID uint) (string, error) {
	if v := s.settingValue(userID, "payments.flutterwave.secretKey"); v != "" {
		return v, nil
	}
	if v := strings.TrimSpace(os.Getenv("FLUTTERWAVE_SECRET_KEY")); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("Flutterwave secret key is not configured")
}

// userEmail is a best-effort lookup used to satisfy Flutterwave's email
// requirement when the caller does not supply one.
func (s *PaymentService) userEmail(userID uint) string {
	var user shared.User
	if err := s.DB.First(&user, userID).Error; err != nil {
		return ""
	}
	return strings.TrimSpace(user.Email)
}

// Charge initiates a mobile money charge through Flutterwave.
func (s *PaymentService) Charge(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	var body ChargeRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	method := strings.ToLower(strings.TrimSpace(body.PaymentMethod))
	if method == "" {
		method = strings.ToLower(strings.TrimSpace(body.Method))
	}
	if method == "" {
		method = strings.ToLower(strings.TrimSpace(body.Type))
	}
	if method == "" {
		method = "mobile_money"
	}
	// Normalise a few common aliases.
	switch method {
	case "mobile", "mobile_money", "mobilemoney", "momo":
		method = "mobile_money"
	case "bank", "bank_transfer", "banktransfer":
		method = "bank_transfer"
	}

	if method == "mobile_money" && strings.TrimSpace(body.Phone) == "" {
		return base.API_ERROR(c, "phone is required")
	}
	if body.Amount <= 0 {
		return base.API_ERROR(c, "amount must be greater than zero")
	}
	if strings.TrimSpace(body.Currency) == "" {
		body.Currency = "UGX"
	}
	if strings.TrimSpace(body.Reference) == "" {
		return base.API_ERROR(c, "reference is required")
	}

	if method == "bank_transfer" {
		cur := strings.ToUpper(strings.TrimSpace(body.Currency))
		if cur != "NGN" && cur != "GHS" {
			return base.APIError(c, fiber.StatusBadRequest, "Bank transfer is not supported for this currency")
		}
	}

	secret, err := s.secretKey(userID)
	if err != nil {
		return base.APIError(c, fiber.StatusBadRequest, err.Error())
	}

	email := strings.TrimSpace(body.Email)
	if email == "" {
		email = s.userEmail(userID)
	}

	chargeType := "mobile_money_uganda"
	if method == "bank_transfer" {
		chargeType = "bank_transfer"
	}

	payload := map[string]interface{}{
		"tx_ref":   body.Reference,
		"amount":   body.Amount,
		"currency": body.Currency,
	}
	if strings.TrimSpace(body.Phone) != "" {
		payload["phone_number"] = body.Phone
	}
	if email != "" {
		payload["email"] = email
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		payload["fullname"] = name
	}
	if desc := strings.TrimSpace(body.Description); desc != "" {
		payload["narration"] = desc
	}

	redirectURL := strings.TrimSpace(body.RedirectURL)
	if redirectURL == "" {
		redirectURL = strings.TrimSpace(body.RedirectUrl)
	}
	if redirectURL != "" {
		payload["meta"] = map[string]interface{}{
			"redirect_url": redirectURL,
		}
	}

	rawBody, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, flutterwaveBaseURL+"/charges?type="+chargeType, bytes.NewReader(rawBody))
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to build payment request")
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return base.APIError(c, fiber.StatusBadGateway, "failed to reach payment gateway")
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)

	var fw struct {
		Status  string        `json:"status"`
		Message string        `json:"message"`
		Meta    *FWChargeMeta `json:"meta"`
		Data    *struct {
			ID     int    `json:"id"`
			FlwRef string `json:"flw_ref"`
			TxRef  string `json:"tx_ref"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respBytes, &fw)

	if resp.StatusCode >= 400 || strings.EqualFold(fw.Status, "error") {
		msg := strings.TrimSpace(fw.Message)
		if msg == "" {
			msg = "payment initiation failed"
		}
		return base.APIError(c, fiber.StatusBadRequest, msg)
	}

	out := FWChargeResponse{
		Status:  fw.Status,
		Message: fw.Message,
		Meta:    fw.Meta,
	}
	if out.Data == nil {
		out.Data = &FWChargeData{}
	}
	if fw.Data != nil {
		out.Data.ID = fw.Data.ID
		if strings.TrimSpace(fw.Data.FlwRef) != "" {
			out.Data.FlwRef = fw.Data.FlwRef
		}
		if strings.TrimSpace(fw.Data.TxRef) != "" {
			out.Data.TxRef = fw.Data.TxRef
		}
	}
	if strings.TrimSpace(out.Data.TxRef) == "" {
		out.Data.TxRef = body.Reference
	}
	// Bank transfer charges return the virtual account details in the
	// authorisation block rather than in `data`; surface them so the UI can
	// show the account to pay into.
	if fw.Meta != nil && fw.Meta.Authorization != nil {
		auth := fw.Meta.Authorization
		if strings.TrimSpace(auth.TransferAccount) != "" {
			out.Data.AccountNumber = auth.TransferAccount
		}
		if strings.TrimSpace(auth.TransferBank) != "" {
			out.Data.BankName = auth.TransferBank
		}
		if strings.TrimSpace(auth.TransferNote) != "" {
			out.Data.Note = auth.TransferNote
		}
		if strings.TrimSpace(auth.AccountExpiration) != "" {
			out.Data.ExpiryDate = auth.AccountExpiration
		}
	}
	if strings.TrimSpace(out.Status) == "" {
		out.Status = "success"
	}
	if strings.TrimSpace(out.Message) == "" {
		out.Message = "Charge initiated"
	}

	return base.OK(c, out, out.Message)
}

// Verify checks the status of a previously initiated transaction.
func (s *PaymentService) Verify(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.APIError(c, fiber.StatusUnauthorized, "user not allowed")
	}

	txRef := strings.TrimSpace(c.Params("txRef"))
	if txRef == "" {
		return base.API_ERROR(c, "tx_ref is required")
	}

	secret, err := s.secretKey(userID)
	if err != nil {
		return base.APIError(c, fiber.StatusBadRequest, err.Error())
	}

	reqURL := flutterwaveBaseURL + "/transactions/verify_by_reference?tx_ref=" + url.QueryEscape(txRef)
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return base.APIError(c, fiber.StatusInternalServerError, "failed to build verification request")
	}
	req.Header.Set("Authorization", "Bearer "+secret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return base.APIError(c, fiber.StatusBadGateway, "failed to reach payment gateway")
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)

	var fw struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Data    *struct {
			Status   string  `json:"status"`
			ID       int     `json:"id"`
			FlwRef   string  `json:"flw_ref"`
			TxRef    string  `json:"tx_ref"`
			Amount   float64 `json:"amount"`
			Currency string  `json:"currency"`
		} `json:"data"`
	}
	_ = json.Unmarshal(respBytes, &fw)

	// Flutterwave returns "No transaction was found for this id" when a
	// transaction has been initiated but not yet completed. That is a
	// transient state, not a failure: surface it as "pending" so callers
	// (and the frontend's pollVerify loop) keep polling instead of aborting.
	if (resp.StatusCode >= 400 || strings.EqualFold(fw.Status, "error")) && !IsTransactionNotFoundMessage(fw.Message) {
		msg := strings.TrimSpace(fw.Message)
		if msg == "" {
			msg = "verification failed"
		}
		return base.APIError(c, fiber.StatusBadRequest, msg)
	}

	out := FWVerifyResponse{
		Status:  fw.Status,
		Message: fw.Message,
	}
	if fw.Data != nil {
		out.Data = &FWVerifyData{
			Status:   fw.Data.Status,
			ID:       fw.Data.ID,
			FlwRef:   fw.Data.FlwRef,
			TxRef:    fw.Data.TxRef,
			Amount:   fw.Data.Amount,
			Currency: fw.Data.Currency,
		}
	}
	if out.Data == nil {
		out.Data = &FWVerifyData{}
	}
	if strings.TrimSpace(out.Data.TxRef) == "" {
		out.Data.TxRef = txRef
	}
	if strings.TrimSpace(out.Data.Status) == "" {
		out.Data.Status = "pending"
	}
	if strings.TrimSpace(out.Status) == "" {
		out.Status = "success"
	}
	if strings.TrimSpace(out.Message) == "" {
		out.Message = "Transaction fetched"
	}

	// Credit the tenant's wallet (append-only ledger) the first time a
	// payment is observed as successful. Repeated verify calls are
	// idempotent: an existing ledger entry with the same tx_ref skips
	// both the transaction append and the company balance bump.
	if strings.EqualFold(out.Data.Status, "successful") {
		var existing transactions.Transaction
		err := s.DB.Where("user_id = ? AND transaction_id = ?", userID, txRef).First(&existing).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return base.APIError(c, fiber.StatusInternalServerError, "failed to check payment record")
		}
		if err == gorm.ErrRecordNotFound {
			txErr := s.DB.Transaction(func(dbtx *gorm.DB) error {
				if _, cErr := transactions.CreateTransaction(dbtx, userID, transactions.Topup, out.Data.Amount, txRef, txRef, transactions.NoteCollection); cErr != nil {
					return cErr
				}
				var company shared.Company
				if cErr := dbtx.Where("user_id = ?", userID).First(&company).Error; cErr == nil {
					bal := ApplyTopupBalance(company.Balance.Data(), out.Data.Amount, out.Data.Currency)
					company.Balance = datatypes.NewJSONType(bal)
					if cErr := dbtx.Save(&company).Error; cErr != nil {
						return cErr
					}
				} else if cErr != gorm.ErrRecordNotFound {
					return cErr
				}
				return nil
			})
			if txErr != nil {
				return base.APIError(c, fiber.StatusInternalServerError, "failed to record payment")
			}
		}
	}

	return base.OK(c, out, out.Message)
}

// ApplyTopupBalance returns the company balance after crediting a successful
// top-up. The amount is rounded to the nearest whole unit (the balance is
// stored as an integer) and the currency is only filled in when it is
// currently empty, so an existing currency preference is never overwritten.
func ApplyTopupBalance(current shared.Balance, amount float64, currency string) shared.Balance {
	updated := current
	updated.Value += int(math.Round(amount))
	if updated.Currency == "" {
		if strings.TrimSpace(currency) != "" {
			updated.Currency = shared.Currency(currency)
		} else {
			updated.Currency = shared.UGX
		}
	}
	return updated
}

// IsTransactionNotFoundMessage reports whether a Flutterwave verification
// error message indicates that the transaction has not been completed yet
// (a transient state) rather than a genuine failure.
func IsTransactionNotFoundMessage(msg string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(msg)), "no transaction was found")
}