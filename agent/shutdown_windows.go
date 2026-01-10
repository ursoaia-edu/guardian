//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func perror(msg string, err error) {
	if err == nil {
		fmt.Println(msg, "OK")
		return
	}
	// for windows syscall errors, err may be syscall.Errno
	if errno, ok := err.(syscall.Errno); ok {
		fmt.Printf("%s: err=%v (errno=%d)\n", msg, err, errno)
	} else {
		fmt.Printf("%s: err=%v\n", msg, err)
	}
}

// shutdownPC initiates a Windows PC shutdown
func shutdownPC() error {
	fmt.Println("start: attempting privilege enable + shutdown")

	advapi32 := windows.NewLazySystemDLL("advapi32.dll")
	procLookupPrivilegeValue := advapi32.NewProc("LookupPrivilegeValueW")
	procAdjustTokenPrivileges := advapi32.NewProc("AdjustTokenPrivileges")
	procInitiateSystemShutdownEx := advapi32.NewProc("InitiateSystemShutdownExW")

	// 1) Open process token
	hProc := windows.CurrentProcess()
	var hToken windows.Token
	err := windows.OpenProcessToken(hProc, windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &hToken)
	perror("OpenProcessToken", err)
	if err != nil {
		return fmt.Errorf("OpenProcessToken failed: %v", err)
	}
	defer hToken.Close()

	// 2) Lookup LUID for SeShutdownPrivilege
	var luid windows.LUID
	privNamePtr, _ := syscall.UTF16PtrFromString("SeShutdownPrivilege")
	r1, _, e1 := procLookupPrivilegeValue.Call(
		0, // lpSystemName = NULL
		uintptr(unsafe.Pointer(privNamePtr)),
		uintptr(unsafe.Pointer(&luid)),
	)
	if r1 == 0 {
		// e1 might be non-nil syscall.Errno
		perror("LookupPrivilegeValueW failed", e1)
		return fmt.Errorf("LookupPrivilegeValueW failed: %v", e1)
	}
	fmt.Println("LookupPrivilegeValueW OK, LUID obtained")

	// 3) Build TOKEN_PRIVILEGES and call AdjustTokenPrivileges
	const SE_PRIVILEGE_ENABLED = 0x00000002
	type tokenpriv struct {
		PrivilegeCount uint32
		Luid           windows.LUID
		Attributes     uint32
	}
	tp := tokenpriv{
		PrivilegeCount: 1,
		Luid:           luid,
		Attributes:     SE_PRIVILEGE_ENABLED,
	}

	r1, _, e1 = procAdjustTokenPrivileges.Call(
		uintptr(hToken),
		0, // DisableAllPrivileges
		uintptr(unsafe.Pointer(&tp)),
		0,
		0,
		0,
	)
	// According to MSDN, AdjustTokenPrivileges returns nonzero even if it fails to enable privileges;
	// must call GetLastError() and check if it is ERROR_SUCCESS.
	if r1 == 0 {
		perror("AdjustTokenPrivileges Call failed", e1)
		return fmt.Errorf("AdjustTokenPrivileges Call failed: %v", e1)
	}
	// check last error
	lastErr := windows.GetLastError()
	if lastErr != syscall.Errno(0) {
		fmt.Printf("AdjustTokenPrivileges reported error (GetLastError=%d)\n", lastErr)
		// Not fatal here — print and continue to see InitiateSystemShutdownEx result
	} else {
		fmt.Println("AdjustTokenPrivileges OK (SeShutdownPrivilege enabled)")
	}

	// 4) Call InitiateSystemShutdownExW
	// signature: BOOL InitiateSystemShutdownExW(
	//   LPWSTR lpMachineName, LPWSTR lpMessage, DWORD dwTimeout, BOOL bForceAppsClosed, BOOL bRebootAfterShutdown, DWORD dwReason
	// );
	r1, _, e1 = procInitiateSystemShutdownEx.Call(
		0,          // lpMachineName = NULL (local)
		0,          // lpMessage = NULL
		uintptr(0), // dwTimeout = 0
		uintptr(1), // bForceAppsClosed = TRUE
		uintptr(0), // bRebootAfterShutdown = FALSE (0 => shutdown/poweroff)
		uintptr(0), // dwReason
	)
	if r1 == 0 {
		perror("InitiateSystemShutdownExW failed", e1)
		// also print GetLastError
		fmt.Printf("InitiateSystemShutdownExW GetLastError: %d\n", windows.GetLastError())
		return fmt.Errorf("InitiateSystemShutdownExW failed: %v", e1)
	}

	fmt.Println("InitiateSystemShutdownExW succeeded — system should be shutting down")

	return nil
}