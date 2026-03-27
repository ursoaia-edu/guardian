# Agent Shutdown Functionality - Testing Guide

## ✅ Changes Made

Fixed the Windows shutdown code in the agent with minimal changes:

1. **Removed broken inline code** (lines 229-256)
2. **Added proper `shutdownPC()` function** with:
   - Correct privilege elevation
   - Proper Windows API calls
   - Graceful shutdown with 30-second warning
   - Fallback to immediate shutdown if needed
   - Error handling

## 🔧 How It Works

When the server sends `"poweroff"` or `"shutdown"` in the blocked applications list, the agent will:

1. Enable `SeShutdownPrivilege` 
2. Call `InitiateSystemShutdownW` with 30-second delay and message
3. If that fails, fallback to `ExitWindowsEx` for immediate forced shutdown

## 🚀 Build & Test

### 1. Build the Agent

```powershell
cd C:\Users\alex\procsentinel\agent

# Clean build
$env:CGO_ENABLED="0"
go build -o procsentinel-agent.exe .
```

### 2. Test in Console Mode (as Administrator)

```powershell
# Run as admin to test
.\procsentinel-agent.exe
```

The agent will:
- Connect to server at `http://localhost:8080`
- Check for blocked applications every 10 seconds
- Shutdown PC if it receives "poweroff" or "shutdown"

### 3. Trigger Shutdown from Server

On the server side, add the shutdown trigger:

```powershell
# Add "shutdown" to blocked apps list
curl -X POST http://localhost:8080/applications `
  -H "Authorization: Bearer mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z" `
  -H "Content-Type: application/json" `
  -d '{"name": "shutdown"}'
```

Or add "poweroff":

```powershell
curl -X POST http://localhost:8080/applications `
  -H "Authorization: Bearer mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z" `
  -H "Content-Type: application/json" `
  -d '{"name": "poweroff"}'
```

### 4. What Should Happen

1. Agent fetches blocked applications from server
2. Sees "shutdown" or "poweroff" in the list
3. Logs: `Shutdown PC triggered: shutdown`
4. Enables shutdown privilege
5. Initiates shutdown with 30-second countdown
6. Windows shows message: "ProcSentinel: Shutdown triggered"
7. System shuts down after 30 seconds

## 🎯 Quick Test Without Actual Shutdown

To test without shutting down, you can:

### Option 1: Test Privilege Elevation Only

Add this test code before the shutdown call:

```go
log.Println("Testing shutdown privilege...")
if err := shutdownPC(); err != nil {
    log.Printf("TEST: Would fail with: %v", err)
} else {
    log.Println("TEST: Shutdown would succeed!")
}
return // Stop here without actual shutdown
```

### Option 2: Abort the Shutdown

After triggering, quickly abort it:

```powershell
# Abort pending shutdown
shutdown /a
```

Or remove "shutdown" from blocked apps before 30 seconds:

```powershell
curl -X DELETE http://localhost:8080/applications/shutdown `
  -H "Authorization: Bearer mILp9n6shk3G9SGSaS2nmP6YlLHwsP1Z"
```

## 📋 Code Changes Summary

### Before (Broken)
```go
// Inline code with wrong API calls
advapi32.NewProc("InitiateSystemShutdownExW").Call(0, 0, 0, 1, 0, 0)
```

### After (Fixed)
```go
// Call proper function
if err := shutdownPC(); err != nil {
    log.Printf("Failed to shutdown PC: %v", err)
}

// Proper implementation
func shutdownPC() error {
    // 1. Enable privilege
    // 2. Call InitiateSystemShutdownW with correct parameters
    // 3. Fallback to ExitWindowsEx if needed
    // 4. Return error if both fail
}
```

## ⚙️ Parameters Used

### InitiateSystemShutdownW
```go
procShutdown.Call(
    0,                                  // Local computer
    uintptr(unsafe.Pointer(message)),   // "ProcSentinel: Shutdown triggered"
    30,                                 // 30 seconds delay
    1,                                  // Force apps to close
    0,                                  // Shutdown (0=shutdown, 1=reboot)
)
```

### ExitWindowsEx (Fallback)
```go
procExit.Call(
    0x00000001 | 0x00000008 | 0x00000004,  // EWX_SHUTDOWN | POWEROFF | FORCE
    0x80000000,                             // SHTDN_REASON_FLAG_PLANNED
)
```

## 🔍 Troubleshooting

### "OpenProcessToken failed"
- **Cause:** Not running as Administrator or Service
- **Solution:** Run as admin or install as service

### "LookupPrivilegeValueW failed"
- **Cause:** System doesn't support the privilege
- **Solution:** Check Windows version (should work on all modern Windows)

### "AdjustTokenPrivileges failed"
- **Cause:** Insufficient permissions
- **Solution:** Ensure running with admin rights

### "InitiateSystemShutdownW failed"
- **Cause:** Another shutdown in progress or privilege issue
- **Solution:** Check logs, fallback to ExitWindowsEx will be attempted

### Shutdown doesn't happen
- **Check logs:** Agent should log "Shutdown PC triggered: shutdown"
- **Verify service:** Make sure agent is running as service or admin
- **Check server:** Verify "shutdown" is in blocked apps list

## 📝 Log Output Examples

### Success:
```
Shutdown PC triggered: shutdown
Shutdown initiated successfully
```

### Privilege Error:
```
Shutdown PC triggered: shutdown
Failed to shutdown PC: OpenProcessToken failed: Access is denied.
```

### Fallback Success:
```
Shutdown PC triggered: shutdown
Graceful shutdown failed, trying immediate shutdown...
Shutdown initiated successfully
```

## 🎉 Production Deployment

Once tested, deploy as service:

```powershell
cd C:\Users\alex\procsentinel\dist

# Rebuild agent
cd ..\agent
$env:CGO_ENABLED="0"
go build -o ..\dist\bin\agent\procsentinel-agent.exe .

# Install as service
cd ..\dist
.\bin\Uninstall-Agent.ps1 -Force
.\bin\Install-Agent-Simple.ps1
```

## ⚠️ Important Notes

1. **Test in safe environment first!**
2. **30-second delay** gives you time to abort
3. **Works only as service or admin** - not as regular user
4. **Trigger keywords:** "shutdown" or "poweroff" in blocked apps list
5. **One-time trigger:** Agent sleeps for 60 seconds after shutdown starts
6. **Fallback mechanism:** Two methods ensure shutdown works

---

Ready to build and test! Remember to run as Administrator.
