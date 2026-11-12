package application

import (
	"context"
	"strings"

	"github.com/kongken/mono/internal/timeline/ent"
	"github.com/kongken/mono/internal/timeline/ent/category"
	"github.com/kongken/mono/internal/timeline/refs"
	timelinev1 "github.com/kongken/mono/pkg/proto/timeline/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var defaultCategoryNames = []string{"餐饮", "交通", "购物", "居住", "医疗", "教育", "娱乐", "运动", "旅行", "工作", "家庭", "宠物", "服饰", "美容", "数码", "礼物", "社交", "订阅", "公益", "投资", "其他支出", "其他"}

func ensureCategories(ctx context.Context, tx *ent.Tx, uid string) error {
	existing, err := tx.Category.Query().Where(category.UserIDEQ(uid)).Select(category.FieldName).Strings(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(existing))
	for _, name := range existing {
		seen[name] = true
	}
	for _, name := range defaultCategoryNames {
		if seen[name] {
			continue
		}
		if _, err := tx.Category.Create().SetPublicRef(refs.New("cat_")).SetUserID(uid).SetName(name).SetActive(true).SetIsDefault(true).SetIsOther(name == "其他").Save(ctx); err != nil {
			return err
		}
	}
	return nil
}

func mapCategory(c *ent.Category) *timelinev1.Category {
	return &timelinev1.Category{CategoryRef: c.PublicRef, Name: c.Name, Active: c.Active, IsDefault: c.IsDefault, IsOther: c.IsOther, Revision: int64(c.Revision), CreatedAt: timestamppb.New(c.CreatedAt), UpdatedAt: timestamppb.New(c.UpdatedAt)}
}

func categorySnapshot(c *ent.Category) ([]byte, error) { return protojson.Marshal(mapCategory(c)) }

func categoryFromSnapshot(b []byte) (*timelinev1.Category, error) {
	c := &timelinev1.Category{}
	err := protojson.Unmarshal(b, c)
	return c, err
}

func (s *TimelineService) ListCategories(ctx context.Context, _ *timelinev1.ListCategoriesRequest) (*timelinev1.ListCategoriesResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := ensureCategories(ctx, tx, uid); err != nil {
		return nil, err
	}
	rows, err := tx.Category.Query().Where(category.UserIDEQ(uid)).Order(ent.Asc(category.FieldName)).All(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := &timelinev1.ListCategoriesResponse{Categories: make([]*timelinev1.Category, 0, len(rows))}
	for _, row := range rows {
		out.Categories = append(out.Categories, mapCategory(row))
	}
	return out, nil
}

func (s *TimelineService) CreateCategory(ctx context.Context, req *timelinev1.CreateCategoryRequest) (*timelinev1.CategoryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 40 || name == "其他" {
		return nil, status.Error(codes.InvalidArgument, "category name is invalid or reserved")
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}
	hash := requestHash(uid, "category_create", "", "", 0, name)
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := ensureCategories(ctx, tx, uid); err != nil {
		return nil, err
	}
	if old, qerr := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "category_create", hash); qerr != nil {
		return nil, qerr
	} else if old != nil {
		c, e := categoryFromSnapshot(old.ResultSnapshot)
		if e != nil {
			return nil, e
		}
		return &timelinev1.CategoryResponse{Category: c, Receipt: receiptResponse(old, true)}, nil
	}
	c, err := tx.Category.Create().SetPublicRef(refs.New("cat_")).SetUserID(uid).SetName(name).SetActive(true).Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, status.Error(codes.AlreadyExists, "category already exists")
		}
		return nil, err
	}
	snap, err := categorySnapshot(c)
	if err != nil {
		return nil, err
	}
	r, err := tx.Receipt.Create().SetUserID(uid).SetMutationID(req.MutationId).SetTargetKind("category").SetOperation("category_create").SetRequestHash(hash).SetEntryRef(c.PublicRef).SetResultRevision(c.Revision).SetResultState("active").SetResultSnapshot(snap).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &timelinev1.CategoryResponse{Category: mapCategory(c), Receipt: receiptResponse(r, false)}, nil
}

func (s *TimelineService) UpdateCategory(ctx context.Context, req *timelinev1.UpdateCategoryRequest) (*timelinev1.CategoryResponse, error) {
	uid, err := user(ctx)
	if err != nil {
		return nil, err
	}
	if !refs.Valid(req.CategoryRef, "cat_") || req.ExpectedRevision <= 0 {
		return nil, status.Error(codes.InvalidArgument, "category_ref and expected_revision are required")
	}
	if err := validateMutationID(req.MutationId); err != nil {
		return nil, err
	}
	hash := requestHash(uid, "category_update", req.CategoryRef, "", req.ExpectedRevision, struct {
		Name   string
		Active bool
	}{nameOr(req.Name), req.Active})
	tx, err := s.db.Tx(ctx)
	if err != nil {
		return nil, err
	}
	if old, qerr := s.priorReceiptTx(ctx, tx, uid, req.MutationId, "category_update", hash); qerr != nil {
		return nil, qerr
	} else if old != nil {
		cat, e := categoryFromSnapshot(old.ResultSnapshot)
		if e != nil {
			return nil, e
		}
		return &timelinev1.CategoryResponse{Category: cat, Receipt: receiptResponse(old, true)}, nil
	}
	defer tx.Rollback()
	c, err := tx.Category.Query().Where(category.UserIDEQ(uid), category.PublicRefEQ(req.CategoryRef)).Only(ctx)
	if ent.IsNotFound(err) {
		return nil, status.Error(codes.NotFound, "category not found")
	}
	if err != nil {
		return nil, err
	}
	if c.IsOther && (!req.Active || (strings.TrimSpace(req.Name) != "" && strings.TrimSpace(req.Name) != c.Name)) {
		return nil, status.Error(codes.InvalidArgument, "其他 cannot be renamed or disabled")
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = c.Name
	}
	if len([]rune(name)) > 40 || name == "其他" && !c.IsOther {
		return nil, status.Error(codes.InvalidArgument, "category name is invalid or reserved")
	}
	updated, err := tx.Category.Update().Where(category.ID(c.ID), category.UserIDEQ(uid), category.RevisionEQ(c.Revision)).SetName(name).SetActive(req.Active).SetRevision(c.Revision + 1).Save(ctx)
	if err != nil {
		return nil, err
	}
	if updated != 1 {
		return nil, status.Error(codes.Aborted, "revision conflict")
	}
	c, err = tx.Category.Query().Where(category.ID(c.ID)).Only(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := categorySnapshot(c)
	if err != nil {
		return nil, err
	}
	r, err := tx.Receipt.Create().SetUserID(uid).SetMutationID(req.MutationId).SetTargetKind("category").SetOperation("category_update").SetRequestHash(hash).SetEntryRef(c.PublicRef).SetResultRevision(c.Revision).SetResultState(map[bool]string{true: "active", false: "inactive"}[c.Active]).SetResultSnapshot(snap).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &timelinev1.CategoryResponse{Category: mapCategory(c), Receipt: receiptResponse(r, false)}, nil
}

func nameOr(name string) string { return strings.TrimSpace(name) }
