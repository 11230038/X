package models

import "time"

// Book is the persistence mapping for a generated book (migration 00021).
//
// SourceText is a snapshot of the material the book was generated from. It is
// deliberately not part of any API projection: the shelf and the detail read
// name the source, never its copy. Only the generation path selects this column.
type Book struct {
	BookID             string     `gorm:"column:book_id;primaryKey;autoIncrement:false"`
	OwnerID            int64      `gorm:"column:owner_id;not null"`
	Title              string     `gorm:"column:title;not null"`
	SourceKind         string     `gorm:"column:source_kind;not null"`
	SourceID           string     `gorm:"column:source_id;not null;default:''"`
	SourceTitle        string     `gorm:"column:source_title;not null;default:''"`
	SourceText         string     `gorm:"column:source_text;not null"`
	SourceChars        int        `gorm:"column:source_chars;not null;default:0"`
	SourceTruncated    bool       `gorm:"column:source_truncated;not null;default:false"`
	OutlineConfirmed   bool       `gorm:"column:outline_confirmed;not null;default:false"`
	OutlineRevision    int        `gorm:"column:outline_revision;not null;default:0"`
	LastOrdinal        int        `gorm:"column:last_ordinal;not null;default:0"`
	ProgressUpdateTime *time.Time `gorm:"column:progress_update_time"`
	CreatedAt          time.Time  `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt          time.Time  `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the books table name.
func (Book) TableName() string { return "books" }

// BookChapter is one row of a book's outline and, once generated, its body.
//
// Content is nullable because "not generated" and "generated to nothing" are
// different states; a failed regeneration keeps the old body by updating only
// the status.
type BookChapter struct {
	ChapterID       string    `gorm:"column:chapter_id;primaryKey;autoIncrement:false"`
	BookID          string    `gorm:"column:book_id;not null"`
	Ordinal         int       `gorm:"column:ordinal;not null"`
	Title           string    `gorm:"column:title;not null"`
	Summary         string    `gorm:"column:summary;not null;default:''"`
	Content         *string   `gorm:"column:content"`
	Status          string    `gorm:"column:status;not null;default:'pending'"`
	ErrorCode       string    `gorm:"column:error_code;not null;default:''"`
	OutlineRevision int       `gorm:"column:outline_revision;not null;default:0"`
	CreatedAt       time.Time `gorm:"column:creat_time;autoCreateTime"`
	UpdatedAt       time.Time `gorm:"column:update_time;autoCreateTime;autoUpdateTime"`
}

// TableName fixes the book_chapters table name.
func (BookChapter) TableName() string { return "book_chapters" }
