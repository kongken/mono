package application

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kongken/mono/internal/metro/ent"
	"github.com/kongken/mono/internal/metro/ent/adminauditlog"
	"github.com/kongken/mono/internal/metro/ent/city"
	"github.com/kongken/mono/internal/metro/ent/line"
	"github.com/kongken/mono/internal/metro/ent/rollingstock"
	metrov1 "github.com/kongken/mono/pkg/proto/metro/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// MaxImportRecords is the most records one import may carry.
const MaxImportRecords = 2000

// parseCatalog decodes and checks a metro-catalog/1 document without touching
// the database: syntax, format, duplicates and every per-record rule. Problems
// carry paths such as lines[3].color.
func parseCatalog(text string) (*catalogDoc, *errList) {
	errs := &errList{}
	if len(text) > maxImportSize {
		errs.add("catalog_json", "must be at most %d bytes", maxImportSize)
		return nil, errs
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.DisallowUnknownFields()
	doc := &catalogDoc{}
	if err := dec.Decode(doc); err != nil {
		var typeErr *json.UnmarshalTypeError
		switch {
		case errors.As(err, &typeErr):
			errs.add(typeErr.Field, "has the wrong type: expected %s", typeErr.Type)
		case errors.Is(err, io.EOF):
			errs.add("catalog_json", "is empty")
		default:
			errs.add("catalog_json", "is not valid metro-catalog/1 JSON: %v", err)
		}
		return nil, errs
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		errs.add("catalog_json", "has data after the document")
		return nil, errs
	}
	if doc.Format != CatalogFormat {
		errs.add("format", "must be %q", CatalogFormat)
	}
	if n := len(doc.Cities) + len(doc.RollingStocks) + len(doc.Lines) + len(doc.Topics); n > MaxImportRecords {
		errs.add("catalog_json", "has %d records; at most %d per import", n, MaxImportRecords)
		return nil, errs
	}
	seen := func(kind string, i int, key string, keys map[string]int) {
		if first, dup := keys[key]; dup {
			errs.add(fmt.Sprintf("%s[%d]", kind, i), "repeats the key of %s[%d]", kind, first)
		} else {
			keys[key] = i
		}
	}
	keys := map[string]int{}
	for i := range doc.Cities {
		d := &doc.Cities[i]
		d.normalize()
		d.validate(fmt.Sprintf("cities[%d].", i), errs)
		seen("cities", i, d.Slug, keys)
	}
	keys = map[string]int{}
	for i := range doc.RollingStocks {
		d := &doc.RollingStocks[i]
		d.validate(fmt.Sprintf("rolling_stocks[%d].", i), errs)
		seen("rolling_stocks", i, d.Code, keys)
	}
	keys = map[string]int{}
	for i := range doc.Lines {
		d := &doc.Lines[i]
		d.normalize()
		d.validate(fmt.Sprintf("lines[%d].", i), errs)
		seen("lines", i, d.Code, keys)
	}
	keys = map[string]int{}
	for i := range doc.Topics {
		d := &doc.Topics[i]
		d.normalize()
		d.validate(fmt.Sprintf("topics[%d].", i), errs)
		seen("topics", i, d.Slug, keys)
	}
	return doc, errs
}

// importCount tallies one record kind.
func importCount(c *metrov1.ChangeCounts, created, updated bool) {
	switch {
	case created:
		c.Created++
	case updated:
		c.Updated++
	default:
		c.Unchanged++
	}
}

// ImportCatalog upserts the document by natural key inside one transaction: all
// of it or none of it. Records that would not change are left alone, so
// importing the same document twice writes nothing the second time.
func (s *AdminService) ImportCatalog(ctx context.Context, req *metrov1.ImportCatalogRequest) (*metrov1.ImportCatalogResponse, error) {
	resp := &metrov1.ImportCatalogResponse{
		DryRun: req.GetDryRun(), Cities: &metrov1.ChangeCounts{}, RollingStocks: &metrov1.ChangeCounts{},
		Lines: &metrov1.ChangeCounts{}, Topics: &metrov1.ChangeCounts{},
	}
	doc, errs := parseCatalog(req.GetCatalogJson())
	if !errs.any() {
		var err error
		if resp.CatalogVersion, err = s.importDoc(ctx, doc, req.GetDryRun(), resp, errs); err != nil {
			return nil, err
		}
	}
	if errs.any() {
		if !req.GetDryRun() {
			return nil, invalid(errs.errs)
		}
		resp.Errors = errs.errs
		resp.Cities, resp.RollingStocks, resp.Lines, resp.Topics =
			&metrov1.ChangeCounts{}, &metrov1.ChangeCounts{}, &metrov1.ChangeCounts{}, &metrov1.ChangeCounts{}
	}
	return resp, nil
}

func (s *AdminService) importDoc(ctx context.Context, doc *catalogDoc, dryRun bool, resp *metrov1.ImportCatalogResponse, errs *errList) (int64, error) {
	return s.write(ctx, func(w *writer) error {
		tx := w.tx
		if !dryRun {
			// Take the catalog lock first so two imports (or an import and an
			// edit) cannot both plan against the same state.
			if _, err := tx.CatalogState.Query().ForUpdate().Only(ctx); err != nil {
				return err
			}
		}
		cities, err := tx.City.Query().All(ctx)
		if err != nil {
			return err
		}
		stocks, err := tx.RollingStock.Query().All(ctx)
		if err != nil {
			return err
		}
		lines, err := tx.Line.Query().WithCity().WithRollingStock().All(ctx)
		if err != nil {
			return err
		}
		topics, err := tx.RollingStockTopic.Query().All(ctx)
		if err != nil {
			return err
		}
		cityByKey := map[string]*ent.City{}
		for _, c := range cities {
			cityByKey[c.Slug] = c
		}
		stockByKey := map[string]*ent.RollingStock{}
		for _, r := range stocks {
			stockByKey[r.Code] = r
		}
		lineByKey := map[string]*ent.Line{}
		for _, l := range lines {
			lineByKey[l.Code] = l
		}
		topicByKey := map[string]*ent.RollingStockTopic{}
		for _, t := range topics {
			topicByKey[t.Slug] = t
		}

		// References must resolve inside the file or the catalog.
		inFileCities, inFileStocks := map[string]bool{}, map[string]bool{}
		for _, d := range doc.Cities {
			inFileCities[d.Slug] = true
		}
		for _, d := range doc.RollingStocks {
			inFileStocks[d.Code] = true
		}
		for i, d := range doc.Lines {
			if cityByKey[d.City] == nil && !inFileCities[d.City] {
				errs.add(fmt.Sprintf("lines[%d].city", i), "no city with slug %q in the file or the catalog", d.City)
			}
			if d.RollingStock != "" && stockByKey[d.RollingStock] == nil && !inFileStocks[d.RollingStock] {
				errs.add(fmt.Sprintf("lines[%d].rolling_stock", i), "no rolling stock with code %q in the file or the catalog", d.RollingStock)
			}
		}
		if errs.any() {
			return nil
		}

		cityIDs := map[string]string{}
		for k, c := range cityByKey {
			cityIDs[k] = c.ID
		}
		stockIDs := map[string]string{}
		for k, r := range stockByKey {
			stockIDs[k] = r.ID
		}

		for _, d := range doc.Cities {
			cur := cityByKey[d.Slug]
			switch {
			case cur == nil:
				importCount(resp.Cities, true, false)
				if dryRun {
					continue
				}
				b := tx.City.Create()
				applyCity(b.Mutation(), d)
				created, err := b.Save(ctx)
				if err != nil {
					return err
				}
				cityIDs[d.Slug] = created.ID
				if err = w.record("city.create", entityCity, created.ID, nil, cityDocOf(created)); err != nil {
					return err
				}
			case cityDocOf(cur).equal(d):
				importCount(resp.Cities, false, false)
			default:
				importCount(resp.Cities, false, true)
				if dryRun {
					continue
				}
				b := tx.City.UpdateOneID(cur.ID).AddRevision(1)
				applyCity(b.Mutation(), d)
				updated, err := b.Save(ctx)
				if err != nil {
					return err
				}
				if err = w.record("city.update", entityCity, cur.ID, cityDocOf(cur), cityDocOf(updated)); err != nil {
					return err
				}
			}
		}
		for _, d := range doc.RollingStocks {
			cur := stockByKey[d.Code]
			switch {
			case cur == nil:
				importCount(resp.RollingStocks, true, false)
				if dryRun {
					continue
				}
				b := tx.RollingStock.Create()
				applyStock(b.Mutation(), d, true)
				created, err := b.Save(ctx)
				if err != nil {
					return err
				}
				stockIDs[d.Code] = created.ID
				if err = w.record("rolling_stock.create", entityStock, created.ID, nil, stockDocOf(created)); err != nil {
					return err
				}
			case stockDocOf(cur).equal(d):
				importCount(resp.RollingStocks, false, false)
			default:
				importCount(resp.RollingStocks, false, true)
				if dryRun {
					continue
				}
				b := tx.RollingStock.UpdateOneID(cur.ID).AddRevision(1)
				applyStock(b.Mutation(), d, false)
				updated, err := b.Save(ctx)
				if err != nil {
					return err
				}
				if err = w.record("rolling_stock.update", entityStock, cur.ID, stockDocOf(cur), stockDocOf(updated)); err != nil {
					return err
				}
			}
		}
		for _, d := range doc.Lines {
			cur := lineByKey[d.Code]
			var stockID *string
			if id, ok := stockIDs[d.RollingStock]; ok && d.RollingStock != "" {
				stockID = &id
			}
			switch {
			case cur == nil:
				importCount(resp.Lines, true, false)
				if dryRun {
					continue
				}
				b := tx.Line.Create()
				applyLine(b.Mutation(), d, cityIDs[d.City], stockID, true)
				created, err := b.Save(ctx)
				if err != nil {
					return err
				}
				full, err := loadLine(ctx, tx, created.ID)
				if err != nil {
					return err
				}
				if err = w.record("line.create", entityLine, created.ID, nil, lineDocOf(full)); err != nil {
					return err
				}
			case lineDocOf(cur).equal(d):
				importCount(resp.Lines, false, false)
			default:
				importCount(resp.Lines, false, true)
				if dryRun {
					continue
				}
				b := tx.Line.UpdateOneID(cur.ID).AddRevision(1)
				applyLine(b.Mutation(), d, cityIDs[d.City], stockID, false)
				if _, err = b.Save(ctx); err != nil {
					return err
				}
				full, err := loadLine(ctx, tx, cur.ID)
				if err != nil {
					return err
				}
				if err = w.record("line.update", entityLine, cur.ID, lineDocOf(cur), lineDocOf(full)); err != nil {
					return err
				}
			}
		}
		for _, d := range doc.Topics {
			cur := topicByKey[d.Slug]
			switch {
			case cur == nil:
				importCount(resp.Topics, true, false)
				if dryRun {
					continue
				}
				b := tx.RollingStockTopic.Create()
				applyTopic(b.Mutation(), d)
				created, err := b.Save(ctx)
				if err != nil {
					return err
				}
				if err = w.record("topic.create", entityTopic, created.ID, nil, topicDocOf(created)); err != nil {
					return err
				}
			case topicDocOf(cur).equal(d):
				importCount(resp.Topics, false, false)
			default:
				importCount(resp.Topics, false, true)
				if dryRun {
					continue
				}
				b := tx.RollingStockTopic.UpdateOneID(cur.ID).AddRevision(1)
				applyTopic(b.Mutation(), d)
				updated, err := b.Save(ctx)
				if err != nil {
					return err
				}
				if err = w.record("topic.update", entityTopic, cur.ID, topicDocOf(cur), topicDocOf(updated)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ExportCatalog returns the whole catalog as a metro-catalog/1 document in a
// stable order, so exports diff cleanly and importing one back changes nothing.
func (s *AdminService) ExportCatalog(ctx context.Context, _ *metrov1.ExportCatalogRequest) (*metrov1.ExportCatalogResponse, error) {
	doc := catalogDoc{Format: CatalogFormat, Cities: []cityDoc{}, RollingStocks: []stockDoc{}, Lines: []lineDoc{}, Topics: []topicDoc{}}
	var version int64
	err := readSnapshot(ctx, s.db, func(tx *ent.Tx, v int64) error {
		version = v
		cities, err := tx.City.Query().Order(ent.Asc(city.FieldSortOrder), ent.Asc(city.FieldSlug)).All(ctx)
		if err != nil {
			return err
		}
		for _, c := range cities {
			doc.Cities = append(doc.Cities, cityDocOf(c))
		}
		stocks, err := tx.RollingStock.Query().Order(ent.Asc(rollingstock.FieldCode)).All(ctx)
		if err != nil {
			return err
		}
		for _, r := range stocks {
			doc.RollingStocks = append(doc.RollingStocks, stockDocOf(r))
		}
		lines, err := tx.Line.Query().WithCity().WithRollingStock().Order(ent.Asc(line.FieldCode)).All(ctx)
		if err != nil {
			return err
		}
		for _, l := range lines {
			doc.Lines = append(doc.Lines, lineDocOf(l))
		}
		topics, err := tx.RollingStockTopic.Query().All(ctx)
		if err != nil {
			return err
		}
		for _, t := range topics {
			doc.Topics = append(doc.Topics, topicDocOf(t))
		}
		return nil
	})
	if err != nil {
		return nil, internalError("admin export", err)
	}
	slices.SortFunc(doc.Topics, func(a, b topicDoc) int { return cmp.Compare(a.Slug, b.Slug) })
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err = enc.Encode(doc); err != nil {
		return nil, internalError("admin export encode", err)
	}
	return &metrov1.ExportCatalogResponse{CatalogJson: buf.String(), CatalogVersion: version}, nil
}

// ListAuditLogs pages through the audit log, newest first.
func (s *AdminService) ListAuditLogs(ctx context.Context, req *metrov1.ListAuditLogsRequest) (*metrov1.ListAuditLogsResponse, error) {
	limit, offset, err := page(req.GetPageSize(), req.GetPageToken())
	if err != nil {
		return nil, err
	}
	q := s.db.AdminAuditLog.Query()
	var errs errList
	if t := req.GetEntityType(); t != "" {
		if !slices.Contains([]string{entityCity, entityStock, entityLine, entityTopic}, t) {
			errs.add("entity_type", "must be city, rolling_stock, line or topic")
		}
		q = q.Where(adminauditlog.EntityType(t))
	}
	if id := req.GetEntityId(); id != "" {
		q = q.Where(adminauditlog.EntityID(id))
	}
	if a := req.GetActorUserId(); a != "" {
		q = q.Where(adminauditlog.ActorUserID(a))
	}
	if c := req.GetChannel(); c != "" {
		if c != "web" && c != "mcp" {
			errs.add("channel", "must be web or mcp")
		}
		q = q.Where(adminauditlog.ChannelEQ(adminauditlog.Channel(c)))
	}
	if req.GetFrom() != nil {
		q = q.Where(adminauditlog.CreatedAtGTE(req.GetFrom().AsTime()))
	}
	if req.GetTo() != nil {
		q = q.Where(adminauditlog.CreatedAtLT(req.GetTo().AsTime()))
	}
	if errs.any() {
		return nil, invalid(errs.errs)
	}
	total, err := q.Clone().Count(ctx)
	if err != nil {
		return nil, internalError("admin audit count", err)
	}
	rows, err := q.Order(ent.Desc(adminauditlog.FieldCreatedAt), ent.Desc(adminauditlog.FieldID)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, internalError("admin audit list", err)
	}
	out := &metrov1.ListAuditLogsResponse{NextPageToken: nextToken(offset, limit, total)}
	for _, r := range rows {
		out.Logs = append(out.Logs, &metrov1.AuditLog{
			Id: r.ID, ActorUserId: r.ActorUserID, Channel: string(r.Channel), TokenId: r.TokenID, RequestId: r.RequestID,
			Action: r.Action, EntityType: r.EntityType, EntityId: r.EntityID,
			BeforeJson: r.BeforeJSON, AfterJson: r.AfterJSON, CreatedAt: timestamppb.New(r.CreatedAt),
		})
	}
	return out, nil
}
