package ftpSync

import (
	"github.com/jlaffaye/ftp"
	"os"
	"path"
	"reflect"
	"strings"
	"sync"
	"time"
)

type SyncFileInfo struct {
	LocalFile  string
	RemoteFile string
	NumberTimes int
}

func NewSyncFileInfo(localFile, remoteFile string, numberTimes int) *SyncFileInfo {
	return &SyncFileInfo{
		LocalFile:  localFile,
		RemoteFile: remoteFile,
		NumberTimes: numberTimes,
	}
}

type SyncFtp struct {
	sync.Mutex
	syncFileChannel chan *SyncFileInfo
	syncStopChannel chan bool
	allRemoteFolder map[string]bool
	syncFtpServer   *ftp.ServerConn
}

func (obj *SyncFtp) connectFtpServer() bool {
	fs, err := ftp.DialTimeout(GConfig.FtpServerAddress, time.Second*30)
	if err != nil {
		Logger.Println(err)
		return false
	}

	err = fs.Login(GConfig.FtpServerUser, GConfig.FtpServerPassword)
	if err != nil {
		Logger.Println(err)
		return false
	}

	obj.syncFtpServer = fs

	return true
}

func (obj *SyncFtp) listFtpServerFolder(p string) {
	fileEntryList, err := obj.syncFtpServer.List(p)
	if err != nil {
		obj.syncFtpServer = nil
		Logger.Print(err)
		return
	}

	for _, e := range fileEntryList {
		if e.Type == ftp.EntryTypeFolder {
			tp := path.Join(p, e.Name)
			obj.allRemoteFolder[tp] = true
			obj.listFtpServerFolder(tp)
			Logger.Printf("found folder:%s", tp)
		}
	}
}

func (obj *SyncFtp) Init() {
	obj.syncFtpServer = nil

	if obj.connectFtpServer() == false {
		return
	}

	obj.Refresh()

	obj.syncFileChannel = make(chan *SyncFileInfo, 10)
	obj.syncStopChannel = make(chan bool, 0)

	go func() {
		checkInterval := time.NewTicker(time.Second * time.Duration(10))
		defer func() {
			Logger.Print("syncFtp will stop")
			checkInterval.Stop()
			close(obj.syncStopChannel)
			close(obj.syncFileChannel)

			if obj.syncFtpServer != nil {
				obj.syncFtpServer.Logout()
				obj.syncFtpServer.Quit()
			}
		}()

	E:
		for {
			select {
			case <-checkInterval.C:
				obj.Refresh()
			case syncFile := <-obj.syncFileChannel:
				obj.Put(syncFile.LocalFile, syncFile.RemoteFile, syncFile.NumberTimes)
			case <-obj.syncStopChannel:
				break E
			}
		}

	F:
		for {
			select {
			case syncFile := <-obj.syncFileChannel:
				obj.Put(syncFile.LocalFile, syncFile.RemoteFile, syncFile.NumberTimes)
			default:
				break F
			}
		}
	}()
}

func (obj *SyncFtp) Refresh() {
	obj.Lock()
	defer obj.Unlock()

	if obj.syncFtpServer == nil && obj.connectFtpServer() == false {
		return
	}

	err := obj.syncFtpServer.NoOp()
	if err != nil {
		obj.syncFtpServer = nil
		Logger.Print(err)
		return
	}

	obj.allRemoteFolder = make(map[string]bool, 0)
	obj.listFtpServerFolder("/")

	Logger.Printf("refresh remote folder %v", reflect.ValueOf(obj.allRemoteFolder).MapKeys())
}

func (obj *SyncFtp) Put(localFile, remoteFile string, numberTimes int) {
	obj.Lock()
	defer obj.Unlock()

	oldRemoteFile := remoteFile

	if strings.HasPrefix(remoteFile, "/") {
		remoteFile = remoteFile[1:]
	}

	if len(remoteFile) > 0 && strings.HasSuffix(remoteFile, "/") {
		remoteFile = remoteFile[:len(remoteFile)-1]
	}

	if len(remoteFile) == 0 {
		Logger.Printf("sync remote file %s failed", oldRemoteFile)
		return
	}

	position := strings.LastIndex(remoteFile, "/")
	remoteFileFolder := remoteFile[:position]

	if _, ok := obj.allRemoteFolder[remoteFileFolder]; !ok {
		remoteFileFolders := strings.Split(remoteFileFolder, "/")
		tempRemoteFileFolder := "/"
		for _, f := range remoteFileFolders {
			tempRemoteFileFolder = path.Join(tempRemoteFileFolder, f)
			if _, ok := obj.allRemoteFolder[tempRemoteFileFolder]; !ok {
				err := obj.syncFtpServer.MakeDir(tempRemoteFileFolder)
				if err == nil {
					obj.allRemoteFolder[tempRemoteFileFolder] = true
					Logger.Printf("mkdir %s success", tempRemoteFileFolder)
				} else {
					Logger.Printf("mkdir %s failed", tempRemoteFileFolder)
					break
				}
			}
		}
	}

	if obj.syncFtpServer == nil && obj.connectFtpServer() == false {
		Logger.Printf("sync %s failed can't connected ftp server", localFile)
		obj.Sync(localFile, remoteFile, numberTimes+1)
		return
	}

	reader, err := os.Open(localFile)
	if err != nil {
		Logger.Printf("open %s failed %v", localFile, err)
		return
	}
	defer reader.Close()

	err = obj.syncFtpServer.Stor(remoteFile, reader)
	if err != nil {
		Logger.Printf("sync %s failed %v", localFile, err)
		obj.Sync(localFile, remoteFile, numberTimes + 1)
		return
	}

	Logger.Printf("sync %s to %s success", localFile, remoteFile)
}

func (obj *SyncFtp) Sync(localFile, remoteFile string, numberTimes int) bool {
	if numberTimes > 3 {
		Logger.Printf("sync %s to %s overflow max num", localFile, remoteFile)
		return false
	}

	if obj.syncFtpServer != nil {
		obj.syncFileChannel <- NewSyncFileInfo(localFile, remoteFile, numberTimes)
		return true
	}

	return false
}

func (obj *SyncFtp) Stop() {
	if obj.syncFtpServer != nil {
		obj.syncStopChannel <- true
	}
}

var GSyncFtp = &SyncFtp{}
