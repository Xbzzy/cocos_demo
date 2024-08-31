package simplelog

import (
	"io"
	"os"
	"strings"
	"time"

	"github.com/Xbzzy/client_demo/server_demo/common/rotatefile"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LogConfig struct {
	ToStdOut   bool   `ini:"to_std_out"`
	AppPrefix  string `ini:"app_prefix"`
	LogDir     string `ini:"log_dir"`
	LogName    string `ini:"log_name"`
	LogLevel   string `ini:"log_level"`
	MaxBackups int    `ini:"max_backups"`
	Minute     int    `ini:"minute"`      // 间隔分钟 默认30
	WithCaller bool   `ini:"with_caller"` // 行号
	MaxAge     int    `ini:"max_age"`     // 过期时间 天
}

type LogI interface {
	SetLogLevel(strLevel string)
	GetLogLevel() string
	SetLogId(id int64)
	SetUid(uid uint64)
	GetUid() (uid uint64)
	Clone() LogI

	Init(config *LogConfig) bool
	InfoWF(msg string, fields ...zapcore.Field)
	WarnWF(msg string, fields ...zapcore.Field)
	DebugWF(msg string, fields ...zapcore.Field)
}

const (
	// 日志内的时间格式
	logFmt = "2006:01:02-15:04:05.000000"
)

func InitZapLog(logLv, logDir, logName string, minute, maxAge int) LogI {
	stdoutLog := &ZapLog{}
	if stdoutLog == nil {
		return nil
	}
	config := &LogConfig{
		LogDir:     logDir,
		LogName:    logName,
		LogLevel:   logLv,
		WithCaller: true,
		Minute:     minute,
		MaxAge:     maxAge,
	}

	if stdoutLog.Init(config) {
		return stdoutLog
	}
	return nil
}

type ZapLog struct {
	uid         uint64
	log         *zap.Logger
	logId       int64
	zapLogLevel *zap.AtomicLevel
	encoder     zapcore.Encoder
	writer      io.Writer
}

func (zl *ZapLog) SetLogLevel(strLevel string) {
	_ = zl.zapLogLevel.UnmarshalText([]byte(strLevel))
}

func (zl *ZapLog) GetLogLevel() string {
	return zl.zapLogLevel.String()
}

func (zl *ZapLog) SetLogId(logId int64) {
	zl.logId = logId
}

func (zl *ZapLog) SetUid(uid uint64) {
	zl.uid = uid
}
func (zl *ZapLog) GetUid() (uid uint64) {
	uid = zl.uid
	return
}

func (zl *ZapLog) Clone() LogI {
	out := &ZapLog{}
	out.log = zl.log
	out.zapLogLevel = zl.zapLogLevel
	out.encoder = zl.encoder
	out.writer = zl.writer
	return out
}

func encodeTimeLayout(t time.Time, layout string, enc zapcore.PrimitiveArrayEncoder) {
	type appendTimeEncoder interface {
		AppendTimeLayout(time.Time, string)
	}

	if enc, ok := enc.(appendTimeEncoder); ok {
		enc.AppendTimeLayout(t, layout)
		return
	}

	enc.AppendString(t.Format(layout))
}

func CustomTimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	encodeTimeLayout(t, logFmt, enc)
}

func (zl *ZapLog) Init(config *LogConfig) bool {
	return zl.initZapLogger(config, true)
}

func (zl *ZapLog) initZapLogger(config *LogConfig, disableStacktrace bool) bool {
	encoderCfg := zap.NewProductionEncoderConfig()
	//encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeTime = CustomTimeEncoder

	logFileName := config.LogName
	if !strings.HasSuffix(logFileName, ".log") {
		logFileName += ".log"
	}

	zl.encoder = zapcore.NewJSONEncoder(encoderCfg)
	var logOut io.Writer
	if config.ToStdOut {
		logOut = os.Stdout
	} else {
		logOut = rotatefile.NewRotateFile(config.LogDir, logFileName, config.MaxBackups, config.MaxAge, config.Minute)
	}
	zl.writer = logOut
	l := zap.NewAtomicLevel()
	zl.zapLogLevel = &l

	zl.SetLogLevel(config.LogLevel)

	core := zapcore.NewTee(
		zapcore.NewCore(zl.encoder, zapcore.AddSync(logOut), zl.zapLogLevel),
	)

	var opts []zap.Option
	if config.WithCaller {
		opts = append(opts, zap.AddCaller(), zap.AddCallerSkip(2))
	}
	if !disableStacktrace {
		opts = append(opts, zap.AddStacktrace(zapcore.ErrorLevel))
	}

	//op :=zap.String("appInfo",appPrefix)
	//opts = append(opts,zap.Fields(op))

	zl.log = zap.New(core, opts...)
	//zl.log = NewFkLogger(core, opts...)
	//zl.level = zapLogLevel.Level()
	zl.log = zl.log.Named(config.AppPrefix)

	return true
}

func (zl *ZapLog) AddLog(fields *[]zapcore.Field) {
	*fields = append(*fields, zap.Uint64("uid", zl.uid), zap.Int64("logID", zl.logId))
}

func (zl *ZapLog) DebugWF(msg string, fields ...zapcore.Field) {
	if zl.log == nil {
		return
	}

	zl.AddLog(&fields)

	func() {
		zl.log.Debug(msg, fields...)
	}()
}

func (zl *ZapLog) InfoWF(msg string, fields ...zapcore.Field) {
	if zl.log == nil {
		return
	}

	zl.AddLog(&fields)

	func() {
		zl.log.Info(msg, fields...)
	}()
}

func (zl *ZapLog) WarnWF(msg string, fields ...zapcore.Field) {
	if zl.log == nil {
		return
	}

	zl.AddLog(&fields)

	func() {
		zl.log.Warn(msg, fields...)
	}()
}

func (zl *ZapLog) ErrorWF(msg string, fields ...zapcore.Field) {
	if zl.log == nil {
		return
	}

	zl.AddLog(&fields)

	func() {
		zl.log.Error(msg, fields...)
	}()
}
