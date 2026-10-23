package application

import (
	"context"
	"strconv"

	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/city"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/ent/rollingstock"
	"github.com/kongken/mono/internal/metro/ent/rollingstocktopic"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
)

// ---- cities ----

func (s *AdminService) CreateCity(ctx context.Context, req *metrov1.CreateCityRequest) (*metrov1.CreateCityResponse, error) {
	d := cityFromInput(req.GetCity())
	if err := validated(d, req.GetCity() != nil, "city"); err != nil {
		return nil, err
	}
	var out *ent.City
	version, err := s.write(ctx, func(w *writer) (err error) {
		b := w.tx.City.Create()
		applyCity(b.Mutation(), d)
		if out, err = b.Save(ctx); err != nil {
			return uniqueOr(err, "city.slug")
		}
		return w.record("city.create", entityCity, out.ID, nil, cityDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.CreateCityResponse{City: adminCity(out), CatalogVersion: version}, nil
}

func (s *AdminService) UpdateCity(ctx context.Context, req *metrov1.UpdateCityRequest) (*metrov1.UpdateCityResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	d := cityFromInput(req.GetCity())
	if err := validated(d, req.GetCity() != nil, "city"); err != nil {
		return nil, err
	}
	var out *ent.City
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.City.Query().Where(city.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("city")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		before := cityDocOf(cur)
		if before.equal(d) {
			out = cur
			return nil
		}
		b := w.tx.City.UpdateOneID(cur.ID).AddRevision(1)
		applyCity(b.Mutation(), d)
		if out, err = b.Save(ctx); err != nil {
			return uniqueOr(err, "city.slug")
		}
		return w.record("city.update", entityCity, out.ID, before, cityDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.UpdateCityResponse{City: adminCity(out), CatalogVersion: version}, nil
}

func (s *AdminService) DeleteCity(ctx context.Context, req *metrov1.DeleteCityRequest) (*metrov1.DeleteCityResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.City.Query().Where(city.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("city")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		n, err := w.tx.Line.Query().Where(line.CityID(cur.ID)).Count(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return referenced("city", &metrov1.EntityCount{EntityType: entityLine, Count: int64(n)})
		}
		if err = w.tx.City.DeleteOneID(cur.ID).Exec(ctx); err != nil {
			return err
		}
		return w.record("city.delete", entityCity, cur.ID, cityDocOf(cur), nil)
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.DeleteCityResponse{CatalogVersion: version}, nil
}

// ---- rolling stock ----

func (s *AdminService) CreateRollingStock(ctx context.Context, req *metrov1.CreateRollingStockRequest) (*metrov1.CreateRollingStockResponse, error) {
	d := stockFromInput(req.GetRollingStock())
	if err := validated(d, req.GetRollingStock() != nil, "rolling_stock"); err != nil {
		return nil, err
	}
	var out *ent.RollingStock
	version, err := s.write(ctx, func(w *writer) (err error) {
		b := w.tx.RollingStock.Create()
		applyStock(b.Mutation(), d, true)
		if out, err = b.Save(ctx); err != nil {
			return uniqueOr(err, "rolling_stock.code")
		}
		return w.record("rolling_stock.create", entityStock, out.ID, nil, stockDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.CreateRollingStockResponse{RollingStock: adminStock(out), CatalogVersion: version}, nil
}

func (s *AdminService) UpdateRollingStock(ctx context.Context, req *metrov1.UpdateRollingStockRequest) (*metrov1.UpdateRollingStockResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	d := stockFromInput(req.GetRollingStock())
	if err := validated(d, req.GetRollingStock() != nil, "rolling_stock"); err != nil {
		return nil, err
	}
	var out *ent.RollingStock
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.RollingStock.Query().Where(rollingstock.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("rolling stock")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		before := stockDocOf(cur)
		if before.equal(d) {
			out = cur
			return nil
		}
		b := w.tx.RollingStock.UpdateOneID(cur.ID).AddRevision(1)
		applyStock(b.Mutation(), d, false)
		if out, err = b.Save(ctx); err != nil {
			return uniqueOr(err, "rolling_stock.code")
		}
		return w.record("rolling_stock.update", entityStock, out.ID, before, stockDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.UpdateRollingStockResponse{RollingStock: adminStock(out), CatalogVersion: version}, nil
}

func (s *AdminService) DeleteRollingStock(ctx context.Context, req *metrov1.DeleteRollingStockRequest) (*metrov1.DeleteRollingStockResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.RollingStock.Query().Where(rollingstock.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("rolling stock")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		n, err := w.tx.Line.Query().Where(line.RollingStockID(cur.ID)).Count(ctx)
		if err != nil {
			return err
		}
		if n > 0 {
			return referenced("rolling stock", &metrov1.EntityCount{EntityType: entityLine, Count: int64(n)})
		}
		if err = w.tx.RollingStock.DeleteOneID(cur.ID).Exec(ctx); err != nil {
			return err
		}
		return w.record("rolling_stock.delete", entityStock, cur.ID, stockDocOf(cur), nil)
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.DeleteRollingStockResponse{CatalogVersion: version}, nil
}

// ---- lines ----

func loadLine(ctx context.Context, tx *ent.Tx, id string) (*ent.Line, error) {
	return tx.Line.Query().Where(line.ID(id)).WithCity().WithRollingStock().Only(ctx)
}

func (s *AdminService) CreateLine(ctx context.Context, req *metrov1.CreateLineRequest) (*metrov1.CreateLineResponse, error) {
	d := lineFromInput(req.GetLine())
	if err := validated(d, req.GetLine() != nil, "line"); err != nil {
		return nil, err
	}
	var out *ent.Line
	version, err := s.write(ctx, func(w *writer) error {
		var errs errList
		cityID, stockID, err := resolveRefs(ctx, w.tx, d, "", &errs)
		if err != nil {
			return err
		}
		if errs.any() {
			return invalid(errs.errs)
		}
		b := w.tx.Line.Create()
		applyLine(b.Mutation(), d, cityID, stockID, true)
		created, err := b.Save(ctx)
		if err != nil {
			return uniqueOr(err, "code")
		}
		if out, err = loadLine(ctx, w.tx, created.ID); err != nil {
			return err
		}
		return w.record("line.create", entityLine, out.ID, nil, lineDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.CreateLineResponse{Line: adminLine(out), CatalogVersion: version}, nil
}

func (s *AdminService) UpdateLine(ctx context.Context, req *metrov1.UpdateLineRequest) (*metrov1.UpdateLineResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	d := lineFromInput(req.GetLine())
	if err := validated(d, req.GetLine() != nil, "line"); err != nil {
		return nil, err
	}
	var out *ent.Line
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.Line.Query().Where(line.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("line")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		if d.Code != cur.Code {
			return invalidField("code", "cannot be changed after creation (current code is "+cur.Code+")")
		}
		var errs errList
		cityID, stockID, err := resolveRefs(ctx, w.tx, d, "", &errs)
		if err != nil {
			return err
		}
		if errs.any() {
			return invalid(errs.errs)
		}
		current, err := loadLine(ctx, w.tx, cur.ID)
		if err != nil {
			return err
		}
		before := lineDocOf(current)
		if before.equal(d) {
			out = current
			return nil
		}
		b := w.tx.Line.UpdateOneID(cur.ID).AddRevision(1)
		applyLine(b.Mutation(), d, cityID, stockID, false)
		if _, err = b.Save(ctx); err != nil {
			return err
		}
		if out, err = loadLine(ctx, w.tx, cur.ID); err != nil {
			return err
		}
		return w.record("line.update", entityLine, out.ID, before, lineDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.UpdateLineResponse{Line: adminLine(out), CatalogVersion: version}, nil
}

func (s *AdminService) DeleteLine(ctx context.Context, req *metrov1.DeleteLineRequest) (*metrov1.DeleteLineResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.Line.Query().Where(line.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("line")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		var refs []*metrov1.EntityCount
		for _, guard := range s.guards {
			kind, n, gerr := guard(ctx, w.tx, cur.ID)
			if gerr != nil {
				return gerr
			}
			if n > 0 {
				refs = append(refs, &metrov1.EntityCount{EntityType: kind, Count: n})
			}
		}
		if len(refs) > 0 {
			return referenced("line (set its status to retired instead)", refs...)
		}
		current, err := loadLine(ctx, w.tx, cur.ID)
		if err != nil {
			return err
		}
		if err = w.tx.Line.DeleteOneID(cur.ID).Exec(ctx); err != nil {
			return err
		}
		return w.record("line.delete", entityLine, cur.ID, lineDocOf(current), nil)
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.DeleteLineResponse{CatalogVersion: version}, nil
}

// ---- topics ----

func (s *AdminService) CreateRollingStockTopic(ctx context.Context, req *metrov1.CreateRollingStockTopicRequest) (*metrov1.CreateRollingStockTopicResponse, error) {
	d := topicFromInput(req.GetTopic())
	if err := validated(d, req.GetTopic() != nil, "topic"); err != nil {
		return nil, err
	}
	var out *ent.RollingStockTopic
	version, err := s.write(ctx, func(w *writer) (err error) {
		b := w.tx.RollingStockTopic.Create()
		applyTopic(b.Mutation(), d)
		if out, err = b.Save(ctx); err != nil {
			return uniqueOr(err, "topic.slug")
		}
		return w.record("topic.create", entityTopic, out.ID, nil, topicDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.CreateRollingStockTopicResponse{Topic: adminTopic(out), CatalogVersion: version}, nil
}

func (s *AdminService) UpdateRollingStockTopic(ctx context.Context, req *metrov1.UpdateRollingStockTopicRequest) (*metrov1.UpdateRollingStockTopicResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	d := topicFromInput(req.GetTopic())
	if err := validated(d, req.GetTopic() != nil, "topic"); err != nil {
		return nil, err
	}
	var out *ent.RollingStockTopic
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.RollingStockTopic.Query().Where(rollingstocktopic.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("topic")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		before := topicDocOf(cur)
		if before.equal(d) {
			out = cur
			return nil
		}
		b := w.tx.RollingStockTopic.UpdateOneID(cur.ID).AddRevision(1)
		applyTopic(b.Mutation(), d)
		if out, err = b.Save(ctx); err != nil {
			return uniqueOr(err, "topic.slug")
		}
		return w.record("topic.update", entityTopic, out.ID, before, topicDocOf(out))
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.UpdateRollingStockTopicResponse{Topic: adminTopic(out), CatalogVersion: version}, nil
}

func (s *AdminService) DeleteRollingStockTopic(ctx context.Context, req *metrov1.DeleteRollingStockTopicRequest) (*metrov1.DeleteRollingStockTopicResponse, error) {
	if err := target(req.GetId(), req.GetExpectedRevision()); err != nil {
		return nil, err
	}
	version, err := s.write(ctx, func(w *writer) error {
		cur, err := w.tx.RollingStockTopic.Query().Where(rollingstocktopic.ID(req.GetId())).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			return notFound("topic")
		} else if err != nil {
			return err
		}
		if int64(cur.Revision) != req.GetExpectedRevision() {
			return revisionConflict(cur.Revision)
		}
		if err = w.tx.RollingStockTopic.DeleteOneID(cur.ID).Exec(ctx); err != nil {
			return err
		}
		return w.record("topic.delete", entityTopic, cur.ID, topicDocOf(cur), nil)
	})
	if err != nil {
		return nil, err
	}
	return &metrov1.DeleteRollingStockTopicResponse{CatalogVersion: version}, nil
}

// ---- lists ----

// page turns page_size and page_token into a limit and offset. The token is the
// offset of the next page.
func page(size int32, token string) (limit, offset int, err error) {
	limit = int(size)
	switch {
	case size < 0:
		return 0, 0, invalidField("page_size", "must not be negative")
	case size == 0:
		limit = defaultPage
	case size > maxPage:
		limit = maxPage
	}
	if token != "" {
		offset, err = strconv.Atoi(token)
		if err != nil || offset < 0 {
			return 0, 0, invalidField("page_token", "is not a token this service issued")
		}
	}
	return limit, offset, nil
}

func nextToken(offset, limit int, total int) string {
	if offset+limit < total {
		return strconv.Itoa(offset + limit)
	}
	return ""
}

func (s *AdminService) ListAdminCities(ctx context.Context, req *metrov1.ListAdminCitiesRequest) (*metrov1.ListAdminCitiesResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	q := s.db.City.Query()
	if t := req.GetQuery(); t != "" {
		q = q.Where(city.Or(city.SlugContainsFold(t), city.NameContainsFold(t)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, internalError("admin list cities", err)
	}
	rows, err := q.Order(ent.Asc(city.FieldSortOrder), ent.Asc(city.FieldSlug)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, internalError("admin list cities", err)
	}
	out := &metrov1.ListAdminCitiesResponse{Total: int64(total), NextPageToken: nextToken(offset, limit, total)}
	for _, r := range rows {
		out.Cities = append(out.Cities, adminCity(r))
	}
	return out, nil
}

func (s *AdminService) ListAdminRollingStocks(ctx context.Context, req *metrov1.ListAdminRollingStocksRequest) (*metrov1.ListAdminRollingStocksResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	q := s.db.RollingStock.Query()
	if t := req.GetQuery(); t != "" {
		q = q.Where(rollingstock.CodeContainsFold(t))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, internalError("admin list rolling stocks", err)
	}
	rows, err := q.Order(ent.Asc(rollingstock.FieldCode)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, internalError("admin list rolling stocks", err)
	}
	out := &metrov1.ListAdminRollingStocksResponse{Total: int64(total), NextPageToken: nextToken(offset, limit, total)}
	for _, r := range rows {
		out.RollingStocks = append(out.RollingStocks, adminStock(r))
	}
	return out, nil
}

func (s *AdminService) ListAdminLines(ctx context.Context, req *metrov1.ListAdminLinesRequest) (*metrov1.ListAdminLinesResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	q := s.db.Line.Query()
	if t := req.GetQuery(); t != "" {
		q = q.Where(line.Or(line.CodeContainsFold(t), line.NameContainsFold(t)))
	}
	if c := req.GetCitySlug(); c != "" {
		q = q.Where(line.HasCityWith(city.Slug(c)))
	}
	var statuses []line.Status
	for _, st := range req.GetStatuses() {
		if st == metrov1.LineStatus_LINE_STATUS_UNSPECIFIED {
			continue
		}
		name, ok := lineStatusNames[st]
		if !ok {
			return nil, invalidField("statuses", "unknown line status")
		}
		statuses = append(statuses, name)
	}
	if len(statuses) > 0 {
		q = q.Where(line.StatusIn(statuses...))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, internalError("admin list lines", err)
	}
	rows, err := q.WithCity().WithRollingStock().Order(ent.Asc(line.FieldCode)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, internalError("admin list lines", err)
	}
	out := &metrov1.ListAdminLinesResponse{Total: int64(total), NextPageToken: nextToken(offset, limit, total)}
	for _, r := range rows {
		out.Lines = append(out.Lines, adminLine(r))
	}
	return out, nil
}

func (s *AdminService) ListAdminRollingStockTopics(ctx context.Context, req *metrov1.ListAdminRollingStockTopicsRequest) (*metrov1.ListAdminRollingStockTopicsResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	q := s.db.RollingStockTopic.Query()
	if t := req.GetQuery(); t != "" {
		q = q.Where(rollingstocktopic.Or(rollingstocktopic.SlugContainsFold(t), rollingstocktopic.NameContainsFold(t)))
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, internalError("admin list topics", err)
	}
	rows, err := q.Order(ent.Asc(rollingstocktopic.FieldSlug)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, internalError("admin list topics", err)
	}
	out := &metrov1.ListAdminRollingStockTopicsResponse{Total: int64(total), NextPageToken: nextToken(offset, limit, total)}
	for _, r := range rows {
		out.Topics = append(out.Topics, adminTopic(r))
	}
	return out, nil
}
