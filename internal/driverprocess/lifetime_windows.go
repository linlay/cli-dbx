package driverprocess

import (
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

var workerLifetime struct {
	once sync.Once
	job  windows.Handle
	err  error
}

// Join the caller to a private job BEFORE creating a worker. Windows then adds
// each child at process creation, avoiding the orphan window between Start and
// AssignProcessToJobObject. No breakaway flags or inheritable job handles are
// used. Existing enclosing jobs are preserved through Windows nested jobs.
func ensureWorkerLifetime() error {
	workerLifetime.once.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			workerLifetime.err = err
			return
		}
		var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
		if err == nil {
			err = windows.AssignProcessToJobObject(job, windows.CurrentProcess())
		}
		if err != nil {
			windows.CloseHandle(job) // Nothing was assigned; safe to close on failure.
			workerLifetime.err = err
			return
		}
		// Deliberately retain this sole, non-inheritable handle for the entire
		// caller lifetime. Closing it here would also terminate the caller.
		// Normal exit, crashes and TerminateProcess all close it in the kernel,
		// killing workers even if a database driver ignores Context/pipe EOF.
		workerLifetime.job = job
	})
	return workerLifetime.err
}
