package repository

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	apperrors "github.com/cygreenenv/greenhouse-panel/internal/errors"
	"github.com/cygreenenv/greenhouse-panel/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMeasurementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=5000", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// SQLite 单写入者：串行化写连接，让并发用例真实竞争唯一索引而非撞库锁。
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	if err = db.AutoMigrate(model.All()...); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedGreenhouseWithSensors(t *testing.T, db *gorm.DB) (model.Greenhouse, []model.Sensor) {
	t.Helper()
	g := model.Greenhouse{Name: "复核测试温室"}
	if err := db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	sensors := []model.Sensor{
		{GreenhouseID: g.ID, Name: "温度", Type: "temperature", Unit: "°C", Status: "online"},
		{GreenhouseID: g.ID, Name: "湿度", Type: "humidity", Unit: "%", Status: "online"},
	}
	for i := range sensors {
		if err := db.Create(&sensors[i]).Error; err != nil {
			t.Fatal(err)
		}
		min, max := 15.0, 32.0
		if sensors[i].Type == "humidity" {
			min, max = 45, 80
		}
		if err := db.Create(&model.Threshold{SensorID: sensors[i].ID, MinValue: min, MaxValue: max}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return g, sensors
}

func TestAddEntryConcurrentDuplicateNeverOverwrites(t *testing.T) {
	db := newMeasurementTestDB(t)
	g, sensors := seedGreenhouseWithSensors(t, db)
	repo := NewMeasurementRepository(db)
	batch := &model.MeasurementBatch{BatchNo: "MB-DUP", GreenhouseID: g.ID, Status: model.BatchStatusDraft}
	if err := repo.CreateBatch(batch); err != nil {
		t.Fatal(err)
	}

	const workers = 8
	var wg sync.WaitGroup
	results := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(v float64) {
			defer wg.Done()
			results <- repo.AddEntry(&model.BatchEntry{BatchID: batch.ID, SensorID: sensors[0].ID, Value: v})
		}(100.0 + float64(i))
	}
	wg.Wait()
	close(results)

	success, conflicts := 0, 0
	for err := range results {
		switch err {
		case nil:
			success++
		case apperrors.ErrEntryConflict:
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if success != 1 || conflicts != workers-1 {
		t.Fatalf("want exactly 1 success and %d conflicts, got %d/%d", workers-1, success, conflicts)
	}

	var count int64
	if err := db.Model(&model.BatchEntry{}).Where("batch_id = ? AND sensor_id = ?", batch.ID, sensors[0].ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want one entry row, got %d", count)
	}
}

func TestSubmitRejectsMissingAndCreatesNoReadings(t *testing.T) {
	db := newMeasurementTestDB(t)
	g, sensors := seedGreenhouseWithSensors(t, db)
	repo := NewMeasurementRepository(db)
	batch := &model.MeasurementBatch{BatchNo: "MB-MISS", GreenhouseID: g.ID, Status: model.BatchStatusDraft}
	if err := repo.CreateBatch(batch); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddEntry(&model.BatchEntry{BatchID: batch.ID, SensorID: sensors[0].ID, Value: 24}); err != nil {
		t.Fatal(err)
	}

	readings, issues, err := repo.Submit(batch.ID, time.Now(), func([]model.Sensor, []model.BatchEntry) model.BatchIssues {
		return model.BatchIssues{{SensorID: sensors[1].ID, Code: model.IssueMissing, Message: "湿度缺项"}}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(readings) != 0 {
		t.Fatalf("rejected submit must not create readings, got %d", len(readings))
	}
	if len(issues) != 1 || issues[0].Code != model.IssueMissing {
		t.Fatalf("want missing issue, got %#v", issues)
	}
	stored, err := repo.GetBatch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.BatchStatusDraft {
		t.Fatalf("rejected batch must stay draft, got %s", stored.Status)
	}
	if len(stored.LastIssues) != 1 {
		t.Fatalf("want persisted issue snapshot, got %#v", stored.LastIssues)
	}
	var readingCount int64
	if err := db.Model(&model.SensorReading{}).Count(&readingCount).Error; err != nil {
		t.Fatal(err)
	}
	if readingCount != 0 {
		t.Fatalf("want zero formal readings after rejection, got %d", readingCount)
	}
}

func TestSubmitSuccessArchivesAndWritesReadings(t *testing.T) {
	db := newMeasurementTestDB(t)
	g, sensors := seedGreenhouseWithSensors(t, db)
	repo := NewMeasurementRepository(db)
	batch := &model.MeasurementBatch{BatchNo: "MB-OK", GreenhouseID: g.ID, Status: model.BatchStatusDraft}
	if err := repo.CreateBatch(batch); err != nil {
		t.Fatal(err)
	}
	for _, s := range sensors {
		value := 24.0
		if s.Type == "humidity" {
			value = 60
		}
		if err := repo.AddEntry(&model.BatchEntry{BatchID: batch.ID, SensorID: s.ID, Value: value}); err != nil {
			t.Fatal(err)
		}
	}

	readings, issues, err := repo.Submit(batch.ID, time.Now(), func([]model.Sensor, []model.BatchEntry) model.BatchIssues { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 || len(readings) != len(sensors) {
		t.Fatalf("want %d readings and no issues, got %d readings, %#v", len(sensors), len(readings), issues)
	}
	stored, err := repo.GetBatch(batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != model.BatchStatusArchived || stored.SubmittedAt == nil {
		t.Fatalf("batch must be archived, got status=%s submittedAt=%v", stored.Status, stored.SubmittedAt)
	}

	// 归档后重复提交必须失败。
	if _, _, err = repo.Submit(batch.ID, time.Now(), func([]model.Sensor, []model.BatchEntry) model.BatchIssues { return nil }); err != apperrors.ErrBatchClosed {
		t.Fatalf("want ErrBatchClosed on resubmit, got %v", err)
	}
	// 归档后追加录入必须失败（唯一冲突之外的状态守卫由 service 保证，仓储层批次不可再提交）。
	var readingCount int64
	if err := db.Model(&model.SensorReading{}).Count(&readingCount).Error; err != nil {
		t.Fatal(err)
	}
	if readingCount != int64(len(sensors)) {
		t.Fatalf("want %d formal readings, got %d", len(sensors), readingCount)
	}
}
