// Copyright 2020 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package db

import (
	"fmt"
	"time"

	gouuid "github.com/satori/go.uuid"
	"gorm.io/gorm"

	"gogs.io/gogs/internal/cryptoutil"
	"gogs.io/gogs/internal/errutil"
)

// AccessTokensStore is the persistent interface for access tokens.
//
// NOTE: All methods are sorted in alphabetical order.
type AccessTokensStore interface {
	// Create creates a new access token and persist to database.
	// It returns ErrAccessTokenAlreadyExist when an access token
	// with same name already exists for the user.
	Create(userID int64, name string) (*AccessToken, error)
	// DeleteByID deletes the access token by given ID.
	// 🚨 SECURITY: The "userID" is required to prevent attacker
	// deletes arbitrary access token that belongs to another user.
	DeleteByID(userID, id int64) error
	// GetBySHA1 returns the access token with given SHA1.
	// It returns ErrAccessTokenNotExist when not found.
	GetBySHA1(sha1 string) (*AccessToken, error)
	// List returns all access tokens belongs to given user.
	List(userID int64) ([]*AccessToken, error)
	// Save persists all values of given access token.
	// The Updated field is set to current time automatically.
	Save(t *AccessToken) error
}

var AccessTokens AccessTokensStore

// AccessToken is a personal access token.
type AccessToken struct {
	ID     int64
	UserID int64 `xorm:"uid INDEX" gorm:"COLUMN:uid;INDEX"`
	Name   string
	Sha1   string `xorm:"UNIQUE VARCHAR(40)" gorm:"TYPE:VARCHAR(40);UNIQUE"`
	SHA256 string `gorm:"type:VARCHAR(64);unique;not null"`

	Created           time.Time `xorm:"-" gorm:"-" json:"-"`
	CreatedUnix       int64
	Updated           time.Time `xorm:"-" gorm:"-" json:"-"`
	UpdatedUnix       int64
	HasRecentActivity bool `xorm:"-" gorm:"-" json:"-"`
	HasUsed           bool `xorm:"-" gorm:"-" json:"-"`
}

// BeforeCreate implements the GORM create hook.
func (t *AccessToken) BeforeCreate(tx *gorm.DB) error {
	if t.CreatedUnix == 0 {
		t.CreatedUnix = tx.NowFunc().Unix()
	}
	return nil
}

// BeforeUpdate implements the GORM update hook.
func (t *AccessToken) BeforeUpdate(tx *gorm.DB) error {
	t.UpdatedUnix = tx.NowFunc().Unix()
	return nil
}

// AfterFind implements the GORM query hook.
func (t *AccessToken) AfterFind(tx *gorm.DB) error {
	t.Created = time.Unix(t.CreatedUnix, 0).Local()
	t.Updated = time.Unix(t.UpdatedUnix, 0).Local()
	t.HasUsed = t.Updated.After(t.Created)
	t.HasRecentActivity = t.Updated.Add(7 * 24 * time.Hour).After(tx.NowFunc())
	return nil
}

var _ AccessTokensStore = (*accessTokens)(nil)

type accessTokens struct {
	*gorm.DB
}

type ErrAccessTokenAlreadyExist struct {
	args errutil.Args
}

func IsErrAccessTokenAlreadyExist(err error) bool {
	_, ok := err.(ErrAccessTokenAlreadyExist)
	return ok
}

func (err ErrAccessTokenAlreadyExist) Error() string {
	return fmt.Sprintf("access token already exists: %v", err.args)
}

func (db *accessTokens) Create(userID int64, name string) (*AccessToken, error) {
	err := db.Where("uid = ? AND name = ?", userID, name).First(new(AccessToken)).Error
	if err == nil {
		return nil, ErrAccessTokenAlreadyExist{args: errutil.Args{"userID": userID, "name": name}}
	} else if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	token := cryptoutil.SHA1(gouuid.NewV4().String())
	sha256 := cryptoutil.SHA256(token)

	accessToken := &AccessToken{
		UserID: userID,
		Name:   name,
		Sha1:   sha256[:40], // To pass the column unique constraint, keep the length of SHA1.
		SHA256: sha256,
	}
	if err = db.DB.Create(accessToken).Error; err != nil {
		return nil, err
	}

	// Set back the raw access token value, for the sake of the caller.
	accessToken.Sha1 = token
	return accessToken, nil
}

func (db *accessTokens) DeleteByID(userID, id int64) error {
	return db.Where("id = ? AND uid = ?", id, userID).Delete(new(AccessToken)).Error
}

var _ errutil.NotFound = (*ErrAccessTokenNotExist)(nil)

type ErrAccessTokenNotExist struct {
	args errutil.Args
}

func IsErrAccessTokenNotExist(err error) bool {
	_, ok := err.(ErrAccessTokenNotExist)
	return ok
}

func (err ErrAccessTokenNotExist) Error() string {
	return fmt.Sprintf("access token does not exist: %v", err.args)
}

func (ErrAccessTokenNotExist) NotFound() bool {
	return true
}

func (db *accessTokens) GetBySHA1(sha1 string) (*AccessToken, error) {
	sha256 := cryptoutil.SHA256(sha1)
	token := new(AccessToken)
	err := db.Where("sha256 = ?", sha256).First(token).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, ErrAccessTokenNotExist{args: errutil.Args{"sha": sha1}}
		}
		return nil, err
	}
	return token, nil
}

func (db *accessTokens) List(userID int64) ([]*AccessToken, error) {
	var tokens []*AccessToken
	return tokens, db.Where("uid = ?", userID).Order("id ASC").Find(&tokens).Error
}

func (db *accessTokens) Save(t *AccessToken) error {
	return db.DB.Save(t).Error
}
