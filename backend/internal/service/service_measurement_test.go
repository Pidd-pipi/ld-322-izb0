package service

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"github.com/cygreenenv/greenhouse-panel/internal/repository"
	ws "github.com/cygreenenv/greenhouse-panel/internal/websocket"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMeasurementService(t *testing.T) (*MeasurementService, *gorm.DB, model.Greenhouse, []model.Sensor, model.Greenhouse, []model.Sensor) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	makeGreenhouse := func(name string) (model.Greenhouse, []model.Sensor) {
		g := model.Greenhouse{Name: name}
		if err = db.Create(&g).Error; err != nil {
			t.Fatal(err)
		}
		types := []struct {
			typ  string
			name string
			unit string
			min  float64
			max  float64
		}{
			{"temperature", "温度", "°C", 15, 32},
			{"humidity", "湿度", "%", 45, 80},
		}
		sensors := make([]model.Sensor, 0, len(types))
		for _, spec := range types {
			s := model.Sensor{GreenhouseID: g.ID, Name: spec.name, Type: spec.typ, Unit: spec.unit, Status: "online"}
			if err = db.Create(&s).Error; err != nil {
				t.Fatal(err)
			}
			if err = db.Create(&model.Threshold{SensorID: s.ID, MinValue: spec.min, MaxValue: spec.max}).Error; err != nil {
				t.Fatal(err)
			}
			sensors = append(sensors, s)
		}
		return g, sensors
	}
	g1, s1 := makeGreenhouse("复核温室一号")
	g2, s2 := makeGreenhouse("复核温室二号")
	svc := NewMeasurementService(
		repository.NewMeasurementRepository(db),
		repository.NewGreenhouseRepository(db),
		repository.NewSensorRepository(db),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		ws.NewHub(),
	)
	return svc, db, g1, s1, g2, s2
}

func TestCreateBatchAndRejectForeignSensorEntry(t *testing.T) {
	svc, _, g1, _, g2, s2 := newMeasurementService(t)
	batch, err := svc.CreateBatch(g1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if batch.Status != model.BatchStatusDraft || batch.BatchNo == "" {
		t.Fatalf("unexpected batch: %#v", batch)
	}
	// 只允许录入该温室传感器：录入别的温室的传感器必须被拒绝。
	if _, err = svc.AddEntry(batch.ID, s2[0].ID, 24); !errors.Is(err, apperrors.ErrSensorNotInGreenhouse) {
		t.Fatalf("want ErrSensorNotInGreenhouse, got %v", err)
	}
	_ = g2
}

func TestDuplicateEntryDoesNotOverwrite(t *testing.T) {
	svc, _, g, sensors, _, _ := newMeasurementService(t)
	batch, err := svc.CreateBatch(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	first, err := svc.AddEntry(batch.ID, sensors[0].ID, 24)
	if err != nil {
		t.Fatal(err)
	}
	// 重复录入（即使带新值）必须冲突失败，不能覆盖首条。
	if _, err = svc.AddEntry(batch.ID, sensors[0].ID, 99); !errors.Is(err, apperrors.ErrEntryConflict) {
		t.Fatalf("want ErrEntryConflict, got %v", err)
	}
	refreshed, err := svc.GetBatch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refreshed.Entries) != 1 || refreshed.Entries[0].Value != first.Value {
		t.Fatalf("first entry must be preserved, got %#v", refreshed.Entries)
	}
}

func TestSubmitMissingThresholdRejectsWholeBatch(t *testing.T) {
	svc, db, g, sensors, _, _ := newMeasurementService(t)
	batch, err := svc.CreateBatch(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 温度合格；湿度缺项。
	if _, err = svc.AddEntry(batch.ID, sensors[0].ID, 25); err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Submit(batch.ID)
	var rejected *apperrors.BatchRejectedError
	if !errors.As(err, &rejected) {
		t.Fatalf("want BatchRejectedError, got %v", err)
	}
	if len(rejected.Issues) != 1 || rejected.Issues[0].Code != model.IssueMissing {
		t.Fatalf("want one missing issue, got %#v", rejected.Issues)
	}

	// 把缺的湿度补成超阈值值后再次提交：仍整批拒绝，且这次是超阈值。
	if _, err = svc.AddEntry(batch.ID, sensors[1].ID, 10); err != nil {
		t.Fatal(err)
	}
	_, _, err = svc.Submit(batch.ID)
	if !errors.As(err, &rejected) {
		t.Fatalf("want BatchRejectedError again, got %v", err)
	}
	if rejected.Issues[0].Code != model.IssueThreshold {
		t.Fatalf("want threshold issue, got %#v", rejected.Issues)
	}
	var count int64
	if err = db.Model(&model.SensorReading{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("no formal readings may exist after rejections, got %d", count)
	}

	// 修正湿度后提交：归档并写入读数。
	if _, err = svc.CorrectEntry(batch.ID, sensors[1].ID, 60); err != nil {
		t.Fatal(err)
	}
	readings, archived, err := svc.Submit(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 2 || archived.Status != model.BatchStatusArchived {
		t.Fatalf("want 2 readings and archived batch, got %d readings status=%s", len(readings), archived.Status)
	}

	// 归档后追加录入、重复提交都直接失败。
	if _, err = svc.AddEntry(batch.ID, sensors[0].ID, 26); !errors.Is(err, apperrors.ErrBatchClosed) {
		t.Fatalf("want ErrBatchClosed on append, got %v", err)
	}
	if _, _, err = svc.Submit(batch.ID); !errors.Is(err, apperrors.ErrBatchClosed) {
		t.Fatalf("want ErrBatchClosed on resubmit, got %v", err)
	}
}

func TestCheckPersistsIssuesForRefresh(t *testing.T) {
	svc, _, g, sensors, _, _ := newMeasurementService(t)
	batch, err := svc.CreateBatch(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Check(batch.ID); err != nil {
		t.Fatal(err)
	}
	stored, err := svc.GetBatch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.LastIssues) != 2 {
		t.Fatalf("want 2 missing issues persisted, got %#v", stored.LastIssues)
	}
	if _, err = svc.AddEntry(batch.ID, sensors[0].ID, 20); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.AddEntry(batch.ID, sensors[1].ID, 60); err != nil {
		t.Fatal(err)
	}
	issues, err := svc.Check(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("want no issues when complete, got %#v", issues)
	}
	stored, err = svc.GetBatch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.LastIssues) != 0 {
		t.Fatalf("issues snapshot should clear after passing, got %#v", stored.LastIssues)
	}
}
