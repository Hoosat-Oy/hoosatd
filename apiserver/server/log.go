package server

import "github.com/daglabs/btcd/util/panics"
import "github.com/daglabs/btcd/apiserver/logger"

var (
	log   = logger.BackendLog.Logger("REST")
	spawn = panics.GoroutineWrapperFunc(log, logger.BackendLog)
)