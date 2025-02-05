package youtubedl

import (
	"encoding/base64"
	"net/url"
	"testing"

	"github.com/dop251/goja/parser"
	"github.com/stretchr/testify/assert"
)

func TestPlayer(t *testing.T) {
	p, err := NewPlayer()
	if err != nil {
		t.Error(err)
	}

	// Test if we extracted all information.
	assert.NotEmpty(t, p.sig_sc)
	assert.NotZero(t, p.sig_timestamp)
	assert.NotEmpty(t, p.nsig_name)
	assert.NotEmpty(t, p.nsig_sc)
	assert.NotEmpty(t, p.nsig_check)
	assert.NotEmpty(t, p.visitorData)

	// Test if we can decode the visitor data.
	visitorData, err := url.QueryUnescape(p.visitorData)
	assert.NoError(t, err)
	_, err = base64.URLEncoding.DecodeString(visitorData)
	assert.NoError(t, err)

	// Test if we can parse the extracted javascript.
	_, err = parser.ParseFile(nil, "", p.nsig_sc, 0)
	assert.NoError(t, err)

	_, err = parser.ParseFile(nil, "", p.sig_sc, 0)
	assert.NoError(t, err)
}
