// Package merchant represents our merchant apis and especially types.
package merchant

import (
	"github.com/adonese/noebs/ebs_fields"
	"github.com/go-redis/redis/v7"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

//Service is a generic struct to hold all application-level data
type Service struct {
	Redis       *redis.Client
	Db          *gorm.DB
	IP          string
	Logger      *logrus.Logger
	NoebsConfig ebs_fields.NoebsConfig
}

//billChan it is used to asyncronysly parses ebs response to get and assign values to the billers
// such as assigning the name to utility personal payment info
var billChan = make(chan ebs_fields.EBSParserFields)
