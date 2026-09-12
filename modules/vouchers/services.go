package vouchers

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/kigongo-vincent/hotspot-be/modules/base"
	"github.com/kigongo-vincent/hotspot-be/modules/packages"
	"github.com/kigongo-vincent/hotspot-be/modules/shared"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func NewService(db *gorm.DB) *VoucherService {
	return &VoucherService{DB: db}
}

func apiSuccess(c *fiber.Ctx, msg string, data any) error {
	return c.JSON(base.APIResponse{Data: data, Message: msg})
}

func toResponse(v Voucher) VoucherResponse {
	packageName := ""
	if v.Package != nil {
		packageName = v.Package.Name
	}

	var packageID uint
	if v.PackageID != nil {
		packageID = *v.PackageID
	}

	agentName := ""
	if v.Agent != nil {
		agentName = v.Agent.Name
	}

	return VoucherResponse{
		ID:              v.ID,
		CreatedAt:       v.CreatedAt.Format("Mon 02/01/2006 15:04"),
		Username:        v.Username,
		PackageID:       packageID,
		PackageName:     packageName,
		PackagePrice:    strconv.Itoa(datatypes.NewJSONType(v.Package.Price).Data().Data().Value),
		PackageDuration: v.Package.Duration,
		Status:          string(v.Status),
		FirstLogin:      v.FirstLogin,
		ExpiresOn:       v.ExpiresOn,
		UseCase:         v.UseCase,
		Note:            v.Note,
		Format:          v.Format,
		CodeLength:      v.CodeLength,
		AgentID:         v.AgentID,
		AgentName:       agentName,
	}
}

// generateUsername mirrors the frontend's placeholder scheme: an 11-digit
// numeric code, generated server-side on create.
func generateUsername() string {
	return fmt.Sprintf("%011d", secureRandInt63n(90000000000)+10000000000)
}

// generateCode produces a random code of the given length drawn from the
// character set for the given voucher format, matching the "Voucher
// Format" dropdown in the UI. Falls back to alphanumeric-lowercase and a
// length of 8 for unset/invalid input, so a voucher always gets a usable
// code.
func generateCode(format VoucherFormat, length int) (string, error) {
	if !format.IsValid() {
		format = FormatAlphanumericLower
	}
	if length <= 0 {
		length = 8
	}

	charset := format.charset()
	max := big.NewInt(int64(len(charset)))

	out := make([]byte, length)
	for i := range out {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = charset[n.Int64()]
	}
	return string(out), nil
}

// secureRandInt63n returns a cryptographically random int64 in [0, n).
// Used instead of math/rand so generated usernames aren't predictable.
func secureRandInt63n(n int64) int64 {
	v, err := rand.Int(rand.Reader, big.NewInt(n))
	if err != nil {
		// Extremely unlikely; fall back to a time-seeded value rather than
		// panicking on username generation.
		return time.Now().UnixNano() % n
	}
	return v.Int64()
}

var durationPattern = regexp.MustCompile(`^(\d+)\s*(HRS|MINS|DAYS)$`)

// durationFromPackage parses the package module's "<amount> <UNIT>" duration
// format (e.g. "8 HRS", "7 DAYS") into a time.Duration. Falls back to 24
// hours if the string doesn't match — e.g. an empty duration on the
// package — so a voucher always gets a sane expiry rather than none.
func durationFromPackage(raw string) time.Duration {
	match := durationPattern.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(raw)))
	if match == nil {
		return 24 * time.Hour
	}

	amount, err := strconv.Atoi(match[1])
	if err != nil {
		return 24 * time.Hour
	}

	switch match[2] {
	case "MINS":
		return time.Duration(amount) * time.Minute
	case "DAYS":
		return time.Duration(amount) * 24 * time.Hour
	default: // HRS
		return time.Duration(amount) * time.Hour
	}
}

// ownedPackage looks up a package by ID, scoped to the caller, so a
// voucher can never be attached to another user's package.
func (s *VoucherService) ownedPackage(userID, packageID uint) (packages.Package, error) {
	var pkg packages.Package
	err := s.DB.Where("user_id = ?", userID).First(&pkg, packageID).Error
	return pkg, err
}

// resolveAgent validates an optional agent ID against the users table.
// Returns (nil, nil) when no agent was requested. Agents are company
// members (the /users endpoint), not scoped to the caller the way
// packages are, matching how that endpoint is exposed.
func (s *VoucherService) resolveAgent(agentID *uint) (*uint, error) {
	if agentID == nil || *agentID == 0 {
		return nil, nil
	}
	var agent shared.User
	if err := s.DB.First(&agent, *agentID).Error; err != nil {
		return nil, err
	}
	return agentID, nil
}

// FindAll returns every voucher owned by the authenticated user, most
// recently created first, with each voucher's package and agent preloaded.
func (s *VoucherService) FindAll(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var vouchers []Voucher
	if err := s.DB.Preload("Package").Preload("Agent").Where("user_id = ?", userID).Order("id desc").Find(&vouchers).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch vouchers")
	}

	resp := make([]VoucherResponse, 0, len(vouchers))
	for _, v := range vouchers {
		resp = append(resp, toResponse(v))
	}

	return apiSuccess(c, "vouchers fetched", VoucherListResponse{Vouchers: resp})
}

// FindByID returns a single voucher, scoped to the authenticated user.
func (s *VoucherService) FindByID(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var voucher Voucher
	if err := s.DB.Preload("Package").Preload("Agent").Where("user_id = ?", userID).First(&voucher, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "voucher not found")
	}

	return apiSuccess(c, "voucher fetched", toResponse(voucher))
}

// searchColumns lists which Voucher columns free-text Search matches
// against (case-insensitive substring, OR'd together), used by
// ListVouchers below.
var searchColumns = []string{"username", "note"}

// applyFilterColumn applies a single base.FilterColumn to the query.
//
// ASSUMPTION: operators supported are "=", "!=", "is_null", "not_null",
// "in" — adjust this switch if your FilterColumn.Operator vocabulary
// differs. The "trashed" pseudo-column is a special case (see below), not
// a real Voucher column.
func applyFilterColumn(q *gorm.DB, f base.FilterColumn) *gorm.DB {
	switch f.Column {
	case "trashed":
		// ASSUMPTION: "Trash" means gorm soft-deleted rows. gorm's default
		// scope hides them, so selecting only trashed rows needs
		// Unscoped() + an explicit NOT NULL check rather than a normal
		// WHERE on deleted_at. If "Trash" means something else in your
		// schema, replace this case.
		return q.Unscoped().Where("deleted_at IS NOT NULL")
	}

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
		// Unknown operator: skip rather than build a broken query.
		return q
	}
}

func applySearch(q *gorm.DB, search string) *gorm.DB {
	search = strings.TrimSpace(search)
	if search == "" || len(searchColumns) == 0 {
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

// defaultPagination fills in sane defaults when the client omits
// limit/page (both zero-valued on an empty PaginationControls).
func defaultPagination(p base.PaginationControls) base.PaginationControls {
	if p.Limit == 0 {
		p.Limit = 10
	}
	if p.Page == 0 {
		p.Page = 1
	}
	return p
}

// ListVouchers handles POST /vouchers/list. Body: base.APIRequest
// (Pagination + Columns + Search). Scoped to the authenticated user, same
// as FindAll. Returns vouchers through toResponse so the response shape
// matches every other voucher endpoint, with Package/Agent preloaded, and
// Pagination.Total set to the full filtered row count (not just
// len(page)).
//
// ASSUMPTION: transport is POST with base.APIRequest as the JSON body —
// adjust the route registration/method if your convention differs.
func (s *VoucherService) ListVouchers(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var req base.APIRequest
	if err := c.BodyParser(&req); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}
	req.Pagination = defaultPagination(req.Pagination)

	query := s.DB.Model(&Voucher{}).Where("user_id = ?", userID)

	for _, f := range req.Columns {
		query = applyFilterColumn(query, f)
	}
	query = applySearch(query, req.Search)

	var total int64
	if err := query.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return base.API_ERROR(c, "failed to count vouchers: "+err.Error())
	}

	var vouchers []Voucher
	offset := int((req.Pagination.Page - 1) * req.Pagination.Limit)
	if err := query.
		Preload("Package").
		Preload("Agent").
		Order("id desc").
		Limit(int(req.Pagination.Limit)).
		Offset(offset).
		Find(&vouchers).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch vouchers: "+err.Error())
	}

	resp := make([]VoucherResponse, 0, len(vouchers))
	for _, v := range vouchers {
		resp = append(resp, toResponse(v))
	}

	return c.JSON(base.APIResponse{
		Data:    VoucherListResponse{Vouchers: resp},
		Message: "vouchers fetched",
		Pagination: base.PaginationControls{
			Limit: req.Pagination.Limit,
			Page:  req.Pagination.Page,
			Total: uint(total),
		},
	})
}

// VoucherDayCount is one point on the "Vouchers Generated" bar chart.
type VoucherDayCount struct {
	Day     string `json:"day"`     // e.g. "Mon"
	Date    string `json:"date"`    // e.g. "2026-08-26", for disambiguation across week boundaries
	Created int    `json:"created"` // count of vouchers created on this calendar day
}

// GetVoucherStats handles GET /vouchers/stats and returns voucher-creation
// counts for each of the last 7 calendar days (including today), oldest
// first, zero-filled for days with no vouchers created. Scoped to the
// authenticated user, same as every other endpoint in this file.
//
// ASSUMPTION: grouping is by last 7 calendar days of Voucher.CreatedAt.
func (s *VoucherService) GetVoucherStats(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	windowStart := startOfToday.AddDate(0, 0, -6)

	var vouchers []Voucher
	if err := s.DB.
		Where("user_id = ? AND created_at >= ?", userID, windowStart).
		Find(&vouchers).Error; err != nil {
		return base.API_ERROR(c, "failed to load voucher stats: "+err.Error())
	}

	counts := make(map[string]int, 7)
	for _, v := range vouchers {
		key := v.CreatedAt.Format("2006-01-02")
		counts[key]++
	}

	points := make([]VoucherDayCount, 0, 7)
	for i := 0; i < 7; i++ {
		day := windowStart.AddDate(0, 0, i)
		key := day.Format("2006-01-02")
		points = append(points, VoucherDayCount{
			Day:     day.Format("Mon"),
			Date:    key,
			Created: counts[key],
		})
	}

	return apiSuccess(c, "voucher stats fetched", fiber.Map{"stats": points})
}

// VoucherBatch represents one distinct "note" grouping of vouchers — a
// batch, in the sense the "Download by Note" dropdown means it. A batch
// has no dedicated ID column; it's identified purely by its Note text,
// same as how Note is already used as the PDF filename base in
// VoucherPreviewModal.
type VoucherBatch struct {
	Note      string `json:"note"`
	Count     int    `json:"count"`
	CreatedAt string `json:"createdAt"` // most recent voucher's created_at in this batch, formatted for display
}

// GetVoucherBatches handles GET /vouchers/batches. Returns one entry per
// distinct non-empty Note value among the authenticated user's vouchers,
// each with a count and the most recent creation time in that batch, most
// recently active batch first. Used to populate the "Download by Note"
// dropdown — selecting one fetches that batch's vouchers via
// GetVoucherBatch and opens VoucherPreviewModal.
func (s *VoucherService) GetVoucherBatches(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	type row struct {
		Note      string
		Count     int
		CreatedAt time.Time
	}

	var rows []row
	if err := s.DB.Model(&Voucher{}).
		Select("TRIM(note) as note, count(*) as count, max(created_at) as created_at").
		Where("user_id = ? AND note IS NOT NULL AND TRIM(note) != ''", userID).
		Group("TRIM(note)").
		Order("max(created_at) desc").
		Scan(&rows).Error; err != nil {
		return base.API_ERROR(c, "failed to load voucher batches: "+err.Error())
	}

	batches := make([]VoucherBatch, 0, len(rows))
	for _, r := range rows {
		batches = append(batches, VoucherBatch{
			Note:      r.Note,
			Count:     r.Count,
			CreatedAt: r.CreatedAt.Format("02 Jan 2006 15:04"),
		})
	}

	return apiSuccess(c, "voucher batches fetched", fiber.Map{"batches": batches})
}

// GetVoucherBatch handles GET /vouchers/batches/:note and returns every
// voucher in that batch (i.e. sharing that exact Note), scoped to the
// authenticated user, in the same shape as FindAll/FindByID so the
// response can be fed straight into VoucherPreviewModal.
//
// :note is taken as-is (URL-decoded by Fiber) and matched exactly against
// Note — batches are identified purely by their note text, so this must
// match GetVoucherBatches' grouping precisely.
func (s *VoucherService) GetVoucherBatch(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	// c.Params returns the raw path segment as matched by Fiber's router;
	// depending on Fiber/fasthttp version this is not always fully
	// URL-decoded (particularly around spaces and punctuation), so decode
	// explicitly here rather than relying on that. The frontend sends
	// this via encodeURIComponent, so this is the exact inverse.
	rawNote := c.Params("note")
	note, decErr := url.QueryUnescape(rawNote)
	if decErr != nil {
		note = rawNote // fall back to the raw value rather than failing outright
	}
	note = strings.TrimSpace(note)

	if note == "" {
		return base.API_ERROR(c, "note is required")
	}

	var vouchers []Voucher
	if err := s.DB.Preload("Package").Preload("Agent").
		Where("user_id = ? AND TRIM(note) = ?", userID, note).
		Order("id desc").
		Find(&vouchers).Error; err != nil {
		return base.API_ERROR(c, "failed to fetch voucher batch")
	}

	if len(vouchers) == 0 {
		return base.API_ERROR(c, "no vouchers found for this note")
	}

	resp := make([]VoucherResponse, 0, len(vouchers))
	for _, v := range vouchers {
		resp = append(resp, toResponse(v))
	}

	return apiSuccess(c, "voucher batch fetched", VoucherListResponse{Vouchers: resp})
}

// Create generates one or more new vouchers attached to one of the
// caller's own packages. Username, status, firstLogin, expiresOn, and each
// voucher's code are all set server-side — the client supplies packageId,
// useCase, note, format, codeLength, quantity, and an optional agentId.
//
// When Quantity is 0 or 1, a single voucher is created and returned as
// before. When Quantity > 1, all generated vouchers are returned as a list
// under "vouchers" instead of a single "voucher" object.
func (s *VoucherService) Create(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var body VoucherRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.PackageID == 0 {
		return base.API_ERROR(c, "package is required")
	}

	if body.Format != "" && !body.Format.IsValid() {
		return base.API_ERROR(c, "invalid voucher format")
	}

	pkg, err := s.ownedPackage(userID, body.PackageID)
	if err != nil {
		return base.API_ERROR(c, "package not found")
	}

	agentID, err := s.resolveAgent(body.AgentID)
	if err != nil {
		return base.API_ERROR(c, "agent not found")
	}

	quantity := body.Quantity
	if quantity <= 0 {
		quantity = 1
	}

	now := time.Now()
	expiresOn := now.Add(durationFromPackage(pkg.Duration)).Format("02 Jan 2006 15:04")
	firstLogin := now.Format("02 Jan 2006 15:04")

	vouchersToCreate := make([]Voucher, 0, quantity)
	for i := 0; i < quantity; i++ {
		code, err := generateCode(body.Format, body.CodeLength)
		if err != nil {
			return base.API_ERROR(c, "failed to generate voucher code")
		}

		vouchersToCreate = append(vouchersToCreate, Voucher{
			Username:   code,
			Status:     VoucherProvisioned,
			FirstLogin: firstLogin,
			ExpiresOn:  expiresOn,
			UseCase:    body.UseCase,
			Note:       body.Note,
			Format:     body.Format,
			CodeLength: body.CodeLength,
			PackageID:  &pkg.ID,
			UserID:     &userID,
			AgentID:    agentID,
		})
	}

	if err := s.DB.Create(&vouchersToCreate).Error; err != nil {
		return base.API_ERROR(c, "failed to create voucher")
	}

	resp := make([]VoucherResponse, 0, len(vouchersToCreate))
	for i := range vouchersToCreate {
		vouchersToCreate[i].Package = &pkg
		resp = append(resp, toResponse(vouchersToCreate[i]))
	}

	if quantity == 1 {
		return apiSuccess(c, "voucher generated", resp[0])
	}
	return apiSuccess(c, "vouchers generated", VoucherListResponse{Vouchers: resp})
}

// Update overwrites an existing voucher's editable fields (package, use
// case, note, format, code length, agent), scoped to vouchers owned by the
// authenticated user. If the package changes, the new package must also
// belong to the caller. Changing format/codeLength does not regenerate the
// existing code — those fields only affect future generation via Create.
func (s *VoucherService) Update(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var voucher Voucher
	if err := s.DB.Where("user_id = ?", userID).First(&voucher, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "voucher not found")
	}

	var body VoucherRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if body.PackageID == 0 {
		return base.API_ERROR(c, "package is required")
	}

	if body.Format != "" && !body.Format.IsValid() {
		return base.API_ERROR(c, "invalid voucher format")
	}

	pkg, err := s.ownedPackage(userID, body.PackageID)
	if err != nil {
		return base.API_ERROR(c, "package not found")
	}

	agentID, err := s.resolveAgent(body.AgentID)
	if err != nil {
		return base.API_ERROR(c, "agent not found")
	}

	voucher.PackageID = &pkg.ID
	voucher.UseCase = body.UseCase
	voucher.Note = body.Note
	if body.Format != "" {
		voucher.Format = body.Format
	}
	if body.CodeLength > 0 {
		voucher.CodeLength = body.CodeLength
	}
	voucher.AgentID = agentID

	if err := s.DB.Save(&voucher).Error; err != nil {
		return base.API_ERROR(c, "failed to update voucher")
	}

	voucher.Package = &pkg
	return apiSuccess(c, "voucher updated", toResponse(voucher))
}

// Delete removes a single voucher, scoped to the authenticated user.
func (s *VoucherService) Delete(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var voucher Voucher
	if err := s.DB.Where("user_id = ?", userID).First(&voucher, c.Params("id")).Error; err != nil {
		return base.API_ERROR(c, "voucher not found")
	}

	if err := s.DB.Delete(&voucher).Error; err != nil {
		return base.API_ERROR(c, "failed to delete voucher")
	}

	return apiSuccess(c, "voucher deleted", nil)
}

// DeleteMany removes several vouchers at once, scoped so a user can only
// delete vouchers they actually own.
func (s *VoucherService) DeleteMany(c *fiber.Ctx) error {
	userID, uErr := base.GetUserIDFromCtx(c)
	if uErr != nil {
		return base.API_ERROR(c, "user not allowed")
	}

	var body DeleteManyRequest
	if err := c.BodyParser(&body); err != nil {
		return base.API_ERROR(c, "failed to parse input")
	}

	if len(body.IDs) == 0 {
		return base.API_ERROR(c, "no ids provided")
	}

	if err := s.DB.Where("user_id = ?", userID).Delete(&Voucher{}, body.IDs).Error; err != nil {
		return base.API_ERROR(c, "failed to delete vouchers")
	}

	return apiSuccess(c, "vouchers deleted", nil)
}
