// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package odbc

import (
	"database/sql/driver"
	"errors"
	"math"
	"sync"

	"github.com/alexbrainman/odbc/api"
)

type Stmt struct {
	c     *Conn
	query string
	os    *ODBCStmt
	mu    sync.Mutex
}

func (c *Conn) Prepare(query string) (driver.Stmt, error) {
	if c.bad {
		return nil, driver.ErrBadConn
	}
	os, err := c.PrepareODBCStmt(query)
	if err != nil {
		return nil, err
	}
	return &Stmt{c: c, os: os, query: query}, nil
}

func (s *Stmt) NumInput() int {
	if s.os == nil {
		return -1
	}
	return len(s.os.Parameters)
}

func (s *Stmt) Close() error {
	if s.os == nil {
		return errors.New("Stmt is already closed")
	}
	ret := s.os.closeByStmt()
	s.os = nil
	return ret
}

func (s *Stmt) Exec(args []driver.Value) (driver.Result, error) {
	if s.os == nil {
		return nil, errors.New("Stmt is closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.os.usedByRows {
		s.os.closeByStmt()
		s.os = nil
		os, err := s.c.PrepareODBCStmt(s.query)
		if err != nil {
			return nil, err
		}
		s.os = os
	}
	err := s.os.Exec(args, s.c)
	if err != nil {
		return nil, err
	}
	var sumRowCount int64
	for {
		var c api.SQLLEN
		ret := api.SQLRowCount(s.os.h, &c)
		if IsError(ret) {
			return nil, NewError("SQLRowCount", s.os.h)
		}
		sumRowCount = accumulateRowCount(sumRowCount, int64(c))
		ret = api.SQLMoreResults(s.os.h)
		if ret == api.SQL_NO_DATA {
			break
		}
		if IsError(ret) {
			return nil, NewError("SQLMoreResults", s.os.h)
		}
	}
	return &Result{rowCount: sumRowCount}, nil
}

func accumulateRowCount(current, next int64) int64 {
	if current < 0 || next < 0 || current > math.MaxInt64-next {
		return -1
	}
	return current + next
}

func (s *Stmt) Query(args []driver.Value) (driver.Rows, error) {
	if s.os == nil {
		return nil, errors.New("Stmt is closed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.os.usedByRows {
		s.os.closeByStmt()
		s.os = nil
		os, err := s.c.PrepareODBCStmt(s.query)
		if err != nil {
			return nil, err
		}
		s.os = os
	}
	err := s.os.Exec(args, s.c)
	if err != nil {
		return nil, err
	}
	err = s.os.BindColumns()
	if err != nil {
		return nil, err
	}
	s.os.usedByRows = true // now both Stmt and Rows refer to it
	return &Rows{os: s.os}, nil
}
