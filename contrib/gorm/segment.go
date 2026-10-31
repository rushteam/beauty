package gorm

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SegmentStore 是 beauty 号段 ID 生成器(pkg/idgen.SegmentStore)的 GORM/MySQL 实现:把「号段
// 批发」落到一张数据库表上。核心库 pkg/idgen.NewSegment 每次从这里原子地批发一段连续 ID 到
// 内存,之后在内存逐个发号,用光再来批发——DB 压力 = 请求量 / step。
//
// 本类型**不 import beauty 核心**:它的 NextRange 方法签名与 idgen.SegmentStore 接口一致,
// 靠 Go 的结构化接口自动满足(使用方 idgen.NewSegment(store, tag) 即可),从而与框架保持解耦。
//
// 表结构(Segment 模型,默认表名 id_segment):每个 biz_tag 一行,max_id 为已批发到的上界,
// step 为每次批发的长度。批发用「UPDATE max_id = max_id + step」的行级原子自增实现,并发安全、
// 跨 MySQL/SQLite 可移植(无需显式 FOR UPDATE)。
type SegmentStore struct {
	db          *gorm.DB
	defaultStep int64
	autoCreate  bool
}

// Segment 是号段表模型。
type Segment struct {
	BizTag    string `gorm:"column:biz_tag;primaryKey;size:128"`
	MaxID     int64  `gorm:"column:max_id;not null;default:0"`
	Step      int64  `gorm:"column:step;not null;default:1000"`
	UpdatedAt time.Time
}

// TableName 指定表名。
func (Segment) TableName() string { return "id_segment" }

// ErrTagNotFound 表示该 biz_tag 在表中不存在且未开启自动创建。
var ErrTagNotFound = errors.New("gorm: id segment tag not found")

// SegmentOption 配置 SegmentStore。
type SegmentOption func(*SegmentStore)

// WithDefaultStep 设置自动创建新 tag 时的号段步长(默认 1000)。
func WithDefaultStep(step int64) SegmentOption {
	return func(s *SegmentStore) {
		if step > 0 {
			s.defaultStep = step
		}
	}
}

// WithAutoCreate 设置遇到未知 tag 时是否自动插入一行(默认 true)。关闭后未知 tag 返回 ErrTagNotFound。
func WithAutoCreate(enabled bool) SegmentOption {
	return func(s *SegmentStore) { s.autoCreate = enabled }
}

// NewSegmentStore 创建 GORM 号段后端。传入的 db 可用 Open/OpenWith 得到的 *gorm.DB
// (DB 嵌入了 *gorm.DB,可直接 store := NewSegmentStore(d.DB))。
func NewSegmentStore(db *gorm.DB, opts ...SegmentOption) *SegmentStore {
	s := &SegmentStore{db: db, defaultStep: 1000, autoCreate: true}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Migrate 自动创建/升级号段表。
func (s *SegmentStore) Migrate() error { return s.db.AutoMigrate(&Segment{}) }

// NextRange 原子地为 tag 批发下一段 [start, start+count)。方法签名与 idgen.SegmentStore 一致。
func (s *SegmentStore) NextRange(ctx context.Context, tag string) (start, count int64, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if s.autoCreate {
			// 不存在则插入初始行(max_id=step),已存在则什么都不做(跨方言的幂等插入)。
			seg := Segment{BizTag: tag, MaxID: s.defaultStep, Step: s.defaultStep}
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&seg)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 1 {
				// 本次刚创建:区间即 [0, step)。
				start, count = 0, s.defaultStep
				return nil
			}
		}
		// 行级原子推进 max_id。
		res := tx.Model(&Segment{}).
			Where("biz_tag = ?", tag).
			UpdateColumn("max_id", gorm.Expr("max_id + step"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrTagNotFound
		}
		var row Segment
		if e := tx.Select("max_id", "step").Where("biz_tag = ?", tag).First(&row).Error; e != nil {
			return e
		}
		start, count = row.MaxID-row.Step, row.Step
		return nil
	})
	if err != nil {
		return 0, 0, err
	}
	return start, count, nil
}
