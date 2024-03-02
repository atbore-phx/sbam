package utils

import (
	"log"

	"go.uber.org/zap"
)

var Log *zap.SugaredLogger

func init() {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	Log = logger.Sugar()
	defer logger.Sync()

}
