#!/usr/bin/env python3
"""把 v2 钩子批量接入真实业务调用点。

mode:
  gate      -> 决策/变换型，放在函数开头，可拦截 403
  emit      -> 通知型，放在函数开头
  defer     -> 完成型通知，函数返回时触发
  deferFail -> 失败型通知，仅状态码 >=400 时触发
  emitNil   -> 无 gin.Context 的内部函数，直接派发
"""
import re
import sys
from pathlib import Path

ROOT = Path(__file__).parent / "backend"

HOOKS_IMPORT = '\t"github.com/nebula-drive/nebula/internal/hooks"\n'
PLUGIN_IMPORT = '\t"github.com/nebula-drive/nebula/pkg/plugin"\n'

# (文件, [(函数名, 钩子常量名, mode), ...])
WIRES = {
    "controllers/auth.go": [
        ("Login", "HookAuthBeforeLogin", "gate"),
        ("Login", "HookAuthProviderResolve", "gate"),
        ("Login", "HookAuthTokenIssue", "gate"),
        ("Login", "HookAuthLoginSuccess", "defer"),
        ("Login", "HookAuthLoginFailed", "deferFail"),
        ("Register", "HookAuthRegisterBefore", "gate"),
        ("Register", "HookAuthRegisterAfter", "defer"),
        ("Logout", "HookAuthLogout", "emit"),
        ("ChangePassword", "HookAuthPasswordChange", "defer"),
        ("Setup2FA", "HookAuth2FAVerify", "gate"),
        ("Confirm2FA", "HookAuth2FAVerify", "gate"),
        ("UpdateProfile", "HookUserUpdate", "defer"),
    ],
    "controllers/file.go": [
        ("Upload", "HookFileTypePolicy", "gate"),
        ("Upload", "HookFileUploadBefore", "gate"),
        ("Upload", "HookContentHashCheck", "gate"),
        ("Upload", "HookContentScan", "gate"),
        ("Upload", "HookFileUploadAfter", "gate"),
        ("Upload", "HookFileUploadFailed", "deferFail"),
        ("Rapid", "HookFileRapidBefore", "gate"),
        ("Rapid", "HookContentHashCheck", "gate"),
        ("ChunkInit", "HookFileChunkInit", "gate"),
        ("ChunkMerge", "HookFileChunkComplete", "defer"),
        ("ChunkMerge", "HookFileUploadBefore", "gate"),
        ("ChunkMerge", "HookFileUploadAfter", "gate"),
        ("Download", "HookFileDownloadBefore", "gate"),
        ("Download", "HookFileDownloadAfter", "defer"),
        ("Rename", "HookFileRename", "gate"),
        ("Move", "HookFileMove", "gate"),
        ("Delete", "HookFileDelete", "gate"),
        ("Restore", "HookFileRestore", "defer"),
        ("Purge", "HookFilePurge", "gate"),
        ("UploadVersion", "HookFileVersionCreate", "defer"),
        ("RestoreVersion", "HookFileVersionRestore", "gate"),
        ("BatchCopy", "HookFileCopy", "gate"),
        ("BatchDelete", "HookFileDelete", "gate"),
        ("AddTag", "HookFileTagChange", "defer"),
        ("RemoveTag", "HookFileTagChange", "defer"),
    ],
    "controllers/preview.go": [
        ("Preview", "HookFilePreview", "gate"),
        ("Thumb", "HookFileThumb", "emit"),
    ],
    "controllers/search.go": [
        ("UpdateTag", "HookFileTagChange", "defer"),
        ("BatchAddTag", "HookFileTagChange", "defer"),
    ],
    "controllers/share.go": [
        ("CreateShare", "HookShareCreateBefore", "gate"),
        ("CreateShare", "HookShareCreateAfter", "defer"),
        ("GetShare", "HookShareAccess", "gate"),
        ("GetShare", "HookShareVerify", "gate"),
        ("DownloadShare", "HookShareDownloadBefore", "gate"),
        ("PreviewShare", "HookShareDownloadBefore", "gate"),
        ("DeleteShare", "HookShareDelete", "defer"),
    ],
    "controllers/task.go": [
        ("CreateTask", "HookTaskCreateBefore", "gate"),
        ("CreateTask", "HookTaskCreateAfter", "defer"),
        ("RetryTask", "HookTaskCreateBefore", "gate"),
        ("CancelTask", "HookTaskCancel", "defer"),
    ],
    "controllers/collab.go": [
        ("CollabOpen", "HookCollabConnect", "emit"),
        ("CollabWS", "HookCollabConnect", "emit"),
        ("CollabWS", "HookCollabDisconnect", "defer"),
    ],
    "controllers/notification.go": [
        ("MarkRead", "HookNotificationRead", "defer"),
        ("MarkAllRead", "HookNotificationRead", "defer"),
    ],
    "controllers/ws.go": [
        ("WSHandler", "HookNotificationSend", "gate"),
    ],
    "controllers/plan.go": [
        ("RedeemCode", "HookCodeRedeemBefore", "gate"),
        ("RedeemCode", "HookCodeRedeemAfter", "defer"),
        ("RedeemCode", "HookPlanSubscribe", "defer"),
    ],
    "controllers/admin.go": [
        ("CreateUser", "HookUserCreate", "defer"),
        ("UpdateUser", "HookUserUpdate", "defer"),
        ("DeleteUser", "HookUserDelete", "defer"),
        ("CreateGroup", "HookGroupChange", "defer"),
        ("UpdateGroup", "HookGroupChange", "defer"),
    ],
    "controllers/admin_ex.go": [
        ("SaveSettings", "HookConfigChanged", "defer"),
        ("CreatePolicy", "HookStoragePolicyChange", "defer"),
        ("UpdatePolicy", "HookStoragePolicyChange", "defer"),
        ("DeletePolicy", "HookStoragePolicyChange", "defer"),
        ("Dashboard", "HookStatsCollect", "gate"),
        ("Dashboard", "HookDashboardQuery", "gate"),
    ],
    "controllers/plugins.go": [
        ("InstallPlugin", "HookPluginInstall", "defer"),
        ("EnablePlugin", "HookPluginEnable", "defer"),
        ("DisablePlugin", "HookPluginDisable", "defer"),
        ("UpdatePluginConfig", "HookPluginConfigChange", "defer"),
    ],
    "webdav/webdav.go": [
        ("serveDAV", "HookWebDAVRequest", "gate"),
        ("propfind", "HookWebDAVPropfind", "gate"),
        ("put", "HookWebDAVPut", "gate"),
        ("delete_", "HookWebDAVDelete", "gate"),
        ("davLock", "HookWebDAVLock", "gate"),
        ("davUnlock", "HookWebDAVLock", "gate"),
    ],
    "filesystem/fs.go": [
        ("New", "HookStorageResolve", "emitNil"),
        ("(l *Local) Put", "HookStorageBeforeWrite", "emitNil"),
        ("(l *Local) Put", "HookStorageAfterWrite", "emitNil"),
        ("(l *Local) Get", "HookStorageBeforeRead", "emitNil"),
    ],
    "migrations/migrate.go": [],  # onDBMigrate 原本已接线
}


def code_for(hook, mode):
    h = "plugin." + hook
    if mode == "gate":
        return (
            f'\t// [plugin-hook] {hook}（决策/变换，可拦截）\n'
            f'\tif __ok, __why, _ := hooks.Gate({h}, c, nil); !__ok {{\n'
            f'\t\tc.JSON(403, gin.H{{"code": 403, "message": __why}})\n'
            f'\t\treturn\n'
            f'\t}}\n'
        )
    if mode == "emit":
        return f'\t// [plugin-hook] {hook}\n\thooks.Emit({h}, hooks.ReqCtx(c, nil))\n'
    if mode == "defer":
        return f'\t// [plugin-hook] {hook}（完成后）\n\tdefer hooks.Emit({h}, hooks.ReqCtx(c, nil))\n'
    if mode == "deferFail":
        return (
            f'\t// [plugin-hook] {hook}（失败时）\n'
            f'\tdefer func() {{\n'
            f'\t\tif c.Writer.Status() >= 400 {{\n'
            f'\t\t\thooks.Emit({h}, hooks.ReqCtx(c, nil))\n'
            f'\t\t}}\n'
            f'\t}}()\n'
        )
    if mode == "emitNil":
        return f'\t// [plugin-hook] {hook}\n\thooks.Emit({h}, nil)\n'
    raise ValueError(mode)


def ensure_imports(src):
    if HOOKS_IMPORT.strip() not in src:
        src = src.replace("import (\n", "import (\n" + HOOKS_IMPORT, 1)
    if PLUGIN_IMPORT.strip() not in src:
        src = src.replace("import (\n", "import (\n" + PLUGIN_IMPORT, 1)
    return src


def func_pattern(name, loose=False):
    # 匹配: func <name>(... *gin.Context ...) {   或  func (recv) <name>(...) {
    if name.startswith("("):
        recv, fn = name.split(") ", 1)
        recv = recv[1:]
        body = r'\s*\([^)]*\)'
        if loose:
            body += r'[^{]*'
        return re.compile(
            r'(func\s*\(' + re.escape(recv) + r'\)\s*' + re.escape(fn) + body + r'\{\n)'
        )
    if loose:
        return re.compile(
            r'(func\s+' + re.escape(name) + r'\s*\([^)]*\)[^{]*\{\n)'
        )
    return re.compile(
        r'(func\s+' + re.escape(name) + r'\s*\([^)]*\*gin\.Context[^)]*\)\s*\{\n)'
    )


def main():
    total = 0
    missing = []
    for rel, wires in WIRES.items():
        p = ROOT / rel
        if not p.exists():
            missing.append(f"文件不存在: {rel}")
            continue
        src = p.read_text(encoding="utf-8")
        for fn, hook, mode in wires:
            pat = func_pattern(fn, loose=(mode == "emitNil"))
            m = pat.search(src)
            if not m:
                missing.append(f"{rel}: 找不到函数 {fn}")
                continue
            block = code_for(hook, mode)
            if block in src:
                continue
            src = src[:m.end()] + block + src[m.end():]
            total += 1
        src = ensure_imports(src)
        p.write_text(src, encoding="utf-8")
    print(f"已插入调用点: {total}")
    if missing:
        print("未匹配:")
        for x in missing:
            print("  -", x)
    return 0 if not missing else 1


if __name__ == "__main__":
    sys.exit(main())
