package utils

import (
	"log"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Log *zap.SugaredLogger

func init() {
	config := zap.NewProductionConfig()
	config.EncoderConfig.EncodeTime = zapcore.RFC3339TimeEncoder
	config.DisableCaller = true
	logger, err := config.Build()
	if err != nil {
		log.Fatalf("can't initialize zap logger: %v", err)
	}
	Log = logger.Sugar()
}
