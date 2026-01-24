// Package merchant represents our merchant apis and especially types.
package merchant

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/adonese/noebs/store"
	"github.com/sirupsen/logrus"
)

// Service is a generic struct to hold all application-level data
type Service struct {
	Store       *store.Store
	IP          string
	Logger      *logrus.Logger
	NoebsConfig ebs_fields.NoebsConfig
}

// billChan it is used to asyncronysly parses ebs response to get and assign values to the billers
// such as assigning the name to utility personal payment info
var billChan = make(chan ebs_fields.EBSParserFields)
