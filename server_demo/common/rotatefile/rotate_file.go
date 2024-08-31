package rotatefile

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Xbzzy/client_demo/server_demo/common/buffer"
)

func NewRotateFile(logPath, logFileName string, MaxBackups, MaxAge int, minute int) (res *RotateFile) {
	if minute <= 0 {
		minute = 30
	}
	if minute > 60 {
		minute = 60
	}

	res = &RotateFile{
		Filename:   logPath + "/" + logFileName,
		MaxBackups: MaxBackups, // 最大文件数
		MaxAge:     MaxAge,     // days 保存时间
		LocalTime:  true,
		SyncLog:    make(chan buffer.IoBuffer, 40960),
		newRule:    true,
		minute:     minute,
	}

	if res.MaxSize == 0 {
		res.MaxSize = defaultMaxSize
	}

	res.cTime = res.getLastTime()

	go res.SyncLogFile()
	go res.Preopen()
	return
}

func chown(_ string, _ os.FileInfo) error {
	return nil
}

const (
	backupTimeFormat = "2006-01-02-15-04-05"
	compressSuffix   = ".gz"
	defaultMaxSize   = 800

	preopenTicker = 1          // second
	preopenTime   = 5          // second  提前 5 秒创建
	preopenSize   = 5          // megabyte  提前 5M 创建
	preopenName   = ".new.log" // 提前创建的日志文件名
)

// ensure we always implement io.WriteCloser
var _ io.WriteCloser = (*RotateFile)(nil)

type RotateFile struct {
	// Filename is the file to write logs to.  Backup log files will be retained
	// in the same directory.  It uses <processname>-lumberjack.log in
	// os.TempDir() if empty.
	Filename string `json:"filename" yaml:"filename"`

	// MaxSize is the maximum size in megabytes of the log file before it gets
	// rotated. It defaults to 100 megabytes.
	MaxSize int `json:"maxsize" yaml:"maxsize"`

	// MaxAge is the maximum number of days to retain old log files based on the
	// timestamp encoded in their filename.  Note that a day is defined as 24
	// hours and may not exactly correspond to calendar days due to daylight
	// savings, leap seconds, etc. The default is not to remove old log files
	// based on age.
	MaxAge int `json:"maxage" yaml:"maxage"`

	// MaxBackups is the maximum number of old log files to retain.  The default
	// is to retain all old log files (though MaxAge may still cause them to get
	// deleted.)
	MaxBackups int `json:"maxbackups" yaml:"maxbackups"`

	// LocalTime determines if the time used for formatting the timestamps in
	// backup files is the computer's local time.  The default is to use UTC
	// time.
	LocalTime bool `json:"localtime" yaml:"localtime"`

	// Compress determines if the rotated log files should be compressed
	// using gzip.
	Compress bool `json:"compress" yaml:"compress"`

	size             int64
	file             *os.File
	newFile, oldFile *os.File
	rotating         int32
	mu               sync.Mutex

	millCh    chan bool
	startMill sync.Once

	SyncLog chan buffer.IoBuffer // 同步日志

	newRule bool //  true 按时间, false 按大小
	minute  int
	cTime   int64 //
	full    bool  //
}

var (
	// currentTime exists so it can be mocked out by tests.
	currentTime = time.Now

	// os_Stat exists so it can be mocked out by tests.
	osStat = os.Stat

	// megabyte is the conversion factor between MaxSize and bytes.  It is a
	// variable so tests can mock it out and not need to write megabytes of data
	// to disk.
	megabyte = 1024 * 1024
)

func (l *RotateFile) SyncLogFile() {
	for b := range l.SyncLog {
		size, _ := b.WriteTo(l.file)
		_ = buffer.PutIoBuffer(b)
		l.size += size
	}
}

// Write implements io.Writer.  If a write would cause the log file to be larger
// than MaxSize, the file is closed, renamed to include a timestamp of the
// current time, and a new log file is created using the original log file name.
// If the length of the write is greater than MaxSize, an error is returned.
func (l *RotateFile) Write(p []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	writeLen := int64(len(p))
	// 按时间分隔时，
	if writeLen > l.max() {
		return 0, fmt.Errorf(
			"write length %d exceeds maximum file size %d", writeLen, l.max(),
		)
	}

	if l.file == nil {
		if err = l.openExistingOrNew(len(p)); err != nil {
			return 0, err
		}
	}

	//
	if l.newRule {
		if l.cTime == 0 {
			l.cTime = l.getLastTime()
		}

		if time.Now().Unix()-l.cTime >= int64(l.minute*60) {
			if err := l.rotate(); err != nil {
				return 0, err
			}
		} else {
			if l.size+writeLen > l.max() {
				l.full = true
				if err := l.rotate(); err != nil {
					return 0, err
				}
			}
		}

	} else {
		if l.size+writeLen > l.max() {
			if err := l.rotate(); err != nil {
				return 0, err
			}
		}
	}

	//

	b := buffer.GetIoBuffer(len(p))
	_, _ = b.Write(p)
	if len(p) == 0 {
		_, _ = b.Write([]byte("\n"))
	}

	select {
	case l.SyncLog <- b:
	default:
	}

	//n, err = l.file.Write(p)
	//l.size += int64(n)

	return n, err
}

// Close implements io.Closer, and closes the current logfile.
func (l *RotateFile) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.close()
}

// close closes the file if it is open.
func (l *RotateFile) close() error {
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}

// Rotate causes RotateFile to close the existing log file and immediately create a
// new one.  This is a helper function for applications that want to initiate
// rotations outside of the normal rotation rules, such as in response to
// SIGHUP.  After rotating, this initiates compression and removal of old log
// files according to the configuration.
func (l *RotateFile) Rotate() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rotate()
}

// rotate closes the current file, moves it aside with a timestamp in the name,
// (if it exists), opens a new file with the original filename, and then runs
// post-rotation processing and removal.
func (l *RotateFile) rotate() error {
	if l.newFile == nil {
		return nil
	}

	if !atomic.CompareAndSwapInt32(&l.rotating, 0, 1) {
		return nil
	}

	fmt.Println("RotateFile rotate begin", l.newFile != nil, l.rotating == 1)
	go l.switchFile(3)

	// if err := l.close(); err != nil {
	// 	return err
	// }
	// if err := l.openNew(); err != nil {
	// 	return err
	// }
	l.mill()
	return nil
}

func (l *RotateFile) switchFile(mask int) {
	_ = os.MkdirAll(l.dir(), 0755)
	time.Sleep(time.Millisecond * 500)

	name := l.filename()
	backName := l.backupName(name)

	if isExist(backName) {
		backName = l.backupNameWith(name)
	}

	if mask&1 != 0 {
		if err := os.Rename(name, backName); err != nil {
			fmt.Println("RotateFile switchFile backup can't rename log file", name, backName, mask, err)
			// l.switchFile(3)
			// return
		}
	}

	if mask&2 != 0 {
		dir := filepath.Dir(l.Filename)
		newName := filepath.Join(dir, preopenName)
		if err := os.Rename(newName, name); err != nil {
			l.newFile = nil
			fmt.Println("RotateFile switchFile adjust can't rename log file", newName, name, mask, err)
			l.switchFile(2)
			return
		}
	}

	l.oldFile = l.file
	l.file = l.newFile
	l.newFile = nil
	l.size = 0
	if l.full {
		l.full = false
	}
	l.cTime = l.getLastTime()
	l.rotating = 0

	if l.oldFile != nil {
		err := l.oldFile.Close()
		if err != nil {
			fmt.Println("RotateFile switchFile close old fail", mask, err)
		}
		l.oldFile = nil
	}

	fmt.Println("RotateFile switchFile success", mask)
}

// Preopen 在切分日志之前,把新日志文件创建出来
func (l *RotateFile) Preopen() {
	_ = os.MkdirAll(l.dir(), 0755)

	ticker := time.NewTicker(time.Duration(preopenTicker) * time.Second)
	mode := os.FileMode(0644)
	dir := filepath.Dir(l.Filename)
	newName := filepath.Join(dir, preopenName)

	for range ticker.C {
		if time.Now().Unix()-l.cTime+preopenTime >= int64(l.minute*60) || l.size+int64(preopenSize*megabyte) > l.max() {
			if l.newFile != nil {
				continue
			}

			f, err := os.OpenFile(newName, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				fmt.Println("RotateFile Preopen cannot open file", newName, err)
				continue
			}

			l.newFile = f
			fmt.Println("RotateFile Preopen success", newName, l.size, time.Now().Unix()-l.cTime)
		}
	}
}

// openNew opens a new log file for writing, moving any old log file out of the
// way.  This methods assumes the file has already been closed.
func (l *RotateFile) openNew() error {
	err := os.MkdirAll(l.dir(), 0744)
	if err != nil {
		return fmt.Errorf("can't make directories for new logfile: %s", err)
	}

	name := l.filename()
	mode := os.FileMode(0644)
	info, err := osStat(name)
	if err == nil {
		// // Copy the mode off the old logfile.
		// mode = info.Mode()
		// move the existing file
		newName := l.backupName(name)

		if isExist(newName) {
			newName = l.backupNameWith(name)
		}

		if err := os.Rename(name, newName); err != nil {
			return fmt.Errorf("can't rename log file: %s", err)
		}

		// this is a no-op anywhere but linux
		if err := chown(name, info); err != nil {
			return err
		}
	}

	// we use truncate here because this should only get called when we've moved
	// the file ourselves. if someone else creates the file in the meantime,
	// just wipe out the contents.
	f, err := os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("can't open new logfile: %s", err)
	}
	l.file = f
	l.size = 0
	if l.newRule {
		if l.full {
			l.full = false
		}
		l.cTime = l.getLastTime()
	}
	return nil
}

func isExist(path string) bool {
	_, err := osStat(path)
	return err == nil || os.IsExist(err)
}

// backupName creates a new filename from the given name, inserting a timestamp
// between the filename and the extension, using the local time if requested
// (otherwise UTC).
func (l *RotateFile) backupName(name string) string {
	dir := filepath.Dir(name)
	filename := filepath.Base(name)
	ext := filepath.Ext(filename)
	prefix := filename[:len(filename)-len(ext)]
	//t := currentTime()
	//if !l.LocalTime {
	//	t = t.UTC()
	//}
	t := int64(0)
	if l.full {
		t = time.Now().Unix()
	} else {
		t = l.cTime + int64(l.minute)*60
	}

	timestamp := time.Unix(t, 0).Format(backupTimeFormat)
	return filepath.Join(dir, fmt.Sprintf("%s-%s%s", prefix, timestamp, ext))
}

func (l *RotateFile) backupNameWith(name string) string {
	dir := filepath.Dir(name)
	filename := filepath.Base(name)
	ext := filepath.Ext(filename)
	prefix := filename[:len(filename)-len(ext)]

	t := int64(0)
	if l.full {
		t = time.Now().Unix()
	} else {
		t = l.cTime + int64(l.minute)*60
	}

	timestamp := time.Unix(t, 0).Format(backupTimeFormat)
	sec := time.Now().Nanosecond() / 1000
	return filepath.Join(dir, fmt.Sprintf("%s-%s.%d%s", prefix, timestamp, sec, ext))
}

// openExistingOrNew opens the logfile if it exists and if the current write
// would not put it over MaxSize.  If there is no such file or the write would
// put it over the MaxSize, a new file is created.
func (l *RotateFile) openExistingOrNew(writeLen int) error {
	l.mill()

	filename := l.filename()
	info, err := osStat(filename)
	if os.IsNotExist(err) {
		return l.openNew()
	}
	if err != nil {
		return fmt.Errorf("error getting log file info: %s", err)
	}

	if !l.newRule && info.Size()+int64(writeLen) >= l.max() {
		return l.rotate()
	}

	file, err := os.OpenFile(filename, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		// if we fail to open the old log file for some reason, just ignore
		// it and open a new log file.
		return l.openNew()
	}
	l.file = file
	l.size = info.Size()
	return nil
}

// genFilename generates the name of the logfile from the current time.
func (l *RotateFile) filename() string {
	if l.Filename != "" {
		return l.Filename
	}
	name := filepath.Base(os.Args[0]) + "-lumberjack.log"
	return filepath.Join(os.TempDir(), name)
}

func ftTime(t int64) string {
	return time.Unix(int64(t), 0).Format("2006-01-02 15:04:05")
}

// millRunOnce performs compression and removal of stale log files.
// Log files are compressed if enabled via configuration and old log
// files are removed, keeping at most l.MaxBackups files, as long as
// none of them are older than MaxAge.
func (l *RotateFile) millRunOnce() error {
	if l.MaxBackups == 0 && l.MaxAge == 0 && !l.Compress {
		return nil
	}

	files, err := l.oldLogFiles()
	if err != nil {
		return err
	}

	var compress, remove []logInfo

	if !l.newRule && l.MaxBackups > 0 && l.MaxBackups < len(files) {
		preserved := make(map[string]struct{})
		var remaining []logInfo
		for _, f := range files {
			// Only count the uncompressed log file or the
			// compressed log file, not both.
			fn := f.Name()
			if strings.HasSuffix(fn, compressSuffix) {
				fn = fn[:len(fn)-len(compressSuffix)]
			}
			preserved[fn] = struct{}{}

			if len(preserved) > l.MaxBackups {
				remove = append(remove, f)
			} else {
				remaining = append(remaining, f)
			}
		}
		files = remaining
	}

	if l.MaxAge > 0 && isRemoveTime() {
		diff := time.Duration(int64(24*time.Hour) * int64(l.MaxAge))
		//diff := time.Duration(int64(time.Minute) * int64(l.MaxAge))
		cutoff := currentTime().Add(-1 * diff)

		var remaining []logInfo
		for _, f := range files {
			if f.timestamp.Before(cutoff) {
				remove = append(remove, f)
			} else {
				remaining = append(remaining, f)
			}
		}
		files = remaining
	}

	if l.Compress {
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), compressSuffix) {
				compress = append(compress, f)
			}
		}
	}

	for _, f := range remove {
		errRemove := os.Remove(filepath.Join(l.dir(), f.Name()))
		if err == nil && errRemove != nil {
			err = errRemove
		}
	}
	for _, f := range compress {
		fn := filepath.Join(l.dir(), f.Name())
		errCompress := compressLogFile(fn, fn+compressSuffix)
		if err == nil && errCompress != nil {
			err = errCompress
		}
	}

	return err
}

func isRemoveTime() bool {
	now := time.Now()
	return 3 <= now.Hour() && now.Hour() <= 7
}

// millRun runs in a goroutine to manage post-rotation compression and removal
// of old log files.
func (l *RotateFile) millRun() {
	for _ = range l.millCh {
		// what am I going to do, log this?
		_ = l.millRunOnce()
	}
}

// mill performs post-rotation compression and removal of stale log files,
// starting the mill goroutine if necessary.
func (l *RotateFile) mill() {
	l.startMill.Do(func() {
		l.millCh = make(chan bool, 1)
		go l.millRun()
	})
	select {
	case l.millCh <- true:
	default:
	}
}

// oldLogFiles returns the list of backup log files stored in the same
// directory as the current log file, sorted by ModTime
func (l *RotateFile) oldLogFiles() ([]logInfo, error) {
	files, err := ioutil.ReadDir(l.dir())
	if err != nil {
		return nil, fmt.Errorf("can't read log file directory: %s", err)
	}
	logFiles := []logInfo{}

	prefix, ext := l.prefixAndExt()

	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if t, err := l.timeFromName(f, prefix, ext); err == nil {
			logFiles = append(logFiles, logInfo{t, f})
			continue
		}
		if t, err := l.timeFromName(f, prefix, ext+compressSuffix); err == nil {
			logFiles = append(logFiles, logInfo{t, f})
			continue
		}
		// error parsing means that the suffix at the end was not generated
		// by lumberjack, and therefore it's not a backup file.
	}

	sort.Sort(byFormatTime(logFiles))

	return logFiles, nil
}

// 上次整点时间
func (l *RotateFile) getLastTime() int64 {
	t := time.Now().Unix()
	res := t % (int64(l.minute) * 60)
	return t - res
}

// timeFromName extracts the formatted time from the filename by stripping off
// the filename's prefix and extension. This prevents someone's filename from
// confusing time.parse.
func (l *RotateFile) timeFromName(f os.FileInfo, prefix, ext string) (time.Time, error) {
	filename := f.Name()
	if !strings.HasPrefix(filename, prefix) {
		return time.Time{}, errors.New("mismatched prefix")
	}
	if !strings.HasSuffix(filename, ext) {
		return time.Time{}, errors.New("mismatched extension")
	}
	//ts := filename[len(prefix) : len(filename)-len(ext)]
	return f.ModTime(), nil
}

// max returns the maximum size in bytes of log files before rolling.
func (l *RotateFile) max() int64 {
	if l.MaxSize == 0 {
		return int64(defaultMaxSize * megabyte)
	}
	return int64(l.MaxSize) * int64(megabyte)
}

// dir returns the directory for the current filename.
func (l *RotateFile) dir() string {
	return filepath.Dir(l.filename())
}

// prefixAndExt returns the filename part and extension part from the RotateFile's
// filename.
func (l *RotateFile) prefixAndExt() (prefix, ext string) {
	filename := filepath.Base(l.filename())
	ext = filepath.Ext(filename)
	prefix = filename[:len(filename)-len(ext)] + "-"
	return prefix, ext
}

// compressLogFile compresses the given log file, removing the
// uncompressed log file if successful.
func compressLogFile(src, dst string) (err error) {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}
	defer f.Close()

	fi, err := osStat(src)
	if err != nil {
		return fmt.Errorf("failed to stat log file: %v", err)
	}

	if err := chown(dst, fi); err != nil {
		return fmt.Errorf("failed to chown compressed log file: %v", err)
	}

	// If this file already exists, we presume it was created by
	// a previous attempt to compress the log file.
	gzf, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, fi.Mode())
	if err != nil {
		return fmt.Errorf("failed to open compressed log file: %v", err)
	}
	defer gzf.Close()

	gz := gzip.NewWriter(gzf)

	defer func() {
		if err != nil {
			os.Remove(dst)
			err = fmt.Errorf("failed to compress log file: %v", err)
		}
	}()

	if _, err := io.Copy(gz, f); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := gzf.Close(); err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		return err
	}

	return nil
}

// logInfo is a convenience struct to return the filename and its embedded
// timestamp.
type logInfo struct {
	timestamp time.Time
	os.FileInfo
}

// byFormatTime sorts by newest time formatted in the name.
type byFormatTime []logInfo

func (b byFormatTime) Less(i, j int) bool {
	return b[i].timestamp.After(b[j].timestamp)
}

func (b byFormatTime) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

func (b byFormatTime) Len() int {
	return len(b)
}
