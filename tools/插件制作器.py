#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
NebulaDrive 插件制作器 · Python 桌面版
=====================================

跨平台：Windows / Linux / macOS，只需要 Python 3.8+ 和标准库 tkinter，
不装任何第三方包，不联网，双击即可运行。

    Windows : 双击本文件，或运行  启动-Windows.bat
    Linux   : python3 插件制作器.py   （无桌面环境可用 --selftest）

三种用法：

    python3 插件制作器.py                 打开图形窗口
    python3 插件制作器.py --selftest     无界面自检：全部模板生成 + 校验 + 打包
    python3 插件制作器.py --build-all DIR  把全部模板导出到 DIR（供联调）
    python3 插件制作器.py --smoke        窗口冒烟测试（1.8 秒后自动退出）

生成物严格对齐宿主插件协议 v2 的三文件契约：
    plugin.json   身份 / 协议 / hooks / pages / providers
    register.go   纯声明文件（宿主只读不编译，服务器不用装 Go）
    co.BEGG       JSON：runtime / requires / home / pages / ui / providers

作者：为 NebulaDrive 社区插件准备。窗口里「帮助 → 新手教程」可随时重看引导。
"""

import copy
import io
import json
import os
import re
import sys
import tempfile
import time
import zipfile
import webbrowser
from pathlib import Path

APP_NAME = "NebulaDrive 插件制作器"
APP_VER = "2.0.0-py"


def _choose_config_path():
    """配置放用户主目录；主目录不可用时回退到脚本同目录（便携/U 盘场景）。"""
    h = Path.home()
    try:
        h.mkdir(parents=True, exist_ok=True)
        return h / ".nebuladrive_plugin_builder.json"
    except Exception:
        return Path(__file__).resolve().parent / ".nebuladrive_plugin_builder.json"


CONFIG_PATH = _choose_config_path()

# ---------------------------------------------------------------------------
# 一、内嵌数据（单文件离线，不需要任何外部 JSON）
# ---------------------------------------------------------------------------

HOOKS = [
    {'name': 'onBoot', 'group': 'system', 'groupLabel': '系统生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': 'bootstrap 完成后触发（DB/迁移/JWT/插件宿主均就绪）。每次启动一次。ctx={version,protocolVersion,dataDir,siteURL}'},
    {'name': 'onShutdown', 'group': 'system', 'groupLabel': '系统生命周期', 'mode': 'sync', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '收到退出信号后、宿主关闭前触发。插件应在此 flush 缓冲。ctx={reason}'},
    {'name': 'onConfigChanged', 'group': 'system', 'groupLabel': '系统生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '后台保存站点设置后触发。ctx={operatorId,operatorName,changed}'},
    {'name': 'onDBMigrate', 'group': 'system', 'groupLabel': '系统生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 1, 'doc': 'AutoMigrate 与版本化迁移完成后触发，每次启动都会触发。插件建表必须幂等。ctx={fromVersion,toVersion}'},
    {'name': 'onScheduledTick', 'group': 'system', 'groupLabel': '系统生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '宿主定时心跳（每 5 分钟）。插件可据此做周期任务，无需另起进程。ctx={tick,at}'},
    {'name': 'onHealthCheck', 'group': 'system', 'groupLabel': '系统生命周期', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': 'GET /api/health 时触发。写 ctx.checks 追加健康项，写 ctx.healthy=false 可返回 503。'},
    {'name': 'onRequestBefore', 'group': 'request', 'groupLabel': 'HTTP 请求', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '所有 /api 请求进入业务前触发。可拦截（WAF/维护模式）、可改写上下文。ctx={method,path,ip,ua,userId,userName,requestId}'},
    {'name': 'onRequestAfter', 'group': 'request', 'groupLabel': 'HTTP 请求', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '请求完成后触发。ctx={method,path,status,durationMs,userId,requestId}'},
    {'name': 'onOpenAPIRequest', 'group': 'request', 'groupLabel': 'HTTP 请求', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '/api/v1/* 开放接口专用，APIAuth 之后触发。ctx={method,path,clientId,userId,scope,ip}'},
    {'name': 'onAuthBeforeLogin', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '校验密码之前触发。可拦截；写 ctx.provider 切换注册型认证源、写 ctx.username 改写登录名。'},
    {'name': 'onAuthProviderResolve', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '注册型(auth)：外部认证源校验。插件写 ctx.ok=true 放行，可附 ctx.claims 注入身份信息。'},
    {'name': 'onAuthLoginSuccess', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '登录成功、会话建立后触发。ctx={userId,userName,ip,ua}'},
    {'name': 'onAuthLoginFailed', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '登录失败触发（含被钩子拦截）。ctx 不含密码。ctx={username,ip,ua,reason}'},
    {'name': 'onAuthLogout', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '登出触发，可联动外置 SSO 单点登出。ctx={userId,userName,ip}'},
    {'name': 'onAuthRegisterBefore', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '注册写入前触发。可拦截；写 ctx.email/ctx.groupId 实现自动分组。'},
    {'name': 'onAuthRegisterAfter', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '注册成功后触发。用于欢迎邮件、初始空间分配。ctx={userId,userName,email}'},
    {'name': 'onAuthPasswordChange', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '修改密码成功后触发。ctx 不含任何密码或哈希。'},
    {'name': 'onAuth2FAVerify', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '2FA 设置/验证时触发，stage=setup|confirm|login。可拦截以接入硬件密钥。'},
    {'name': 'onAuthTokenIssue', 'group': 'auth', 'groupLabel': '认证与会话', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': 'JWT 签发前触发。写 ctx.claims 注入自定义 claim、改 ctx.ttl 调有效期。'},
    {'name': 'onPermissionCheck', 'group': 'user', 'groupLabel': '用户与权限', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '鉴权中间件通过身份校验后触发。可拦截实现 ABAC/部门级权限。高频，走轻量门。'},
    {'name': 'onUserCreate', 'group': 'user', 'groupLabel': '用户与权限', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '管理员创建用户后触发。ctx={operatorId,userId,userName,groupId}'},
    {'name': 'onUserUpdate', 'group': 'user', 'groupLabel': '用户与权限', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '管理员修改用户后触发。ctx={operatorId,userId,changed}'},
    {'name': 'onUserDelete', 'group': 'user', 'groupLabel': '用户与权限', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '管理员删除用户后触发，可做外部账号回收。'},
    {'name': 'onUserQuotaChange', 'group': 'user', 'groupLabel': '用户与权限', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '计算有效配额时触发。写 ctx.maxStorage 覆盖额度（对接外部计费）。source=plan|group|fallback'},
    {'name': 'onGroupChange', 'group': 'user', 'groupLabel': '用户与权限', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '用户组增删改后触发，action=create|update|delete。'},
    {'name': 'onFileTypePolicy', 'group': 'content', 'groupLabel': '内容治理', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '扩展名白名单判定时触发。写 ctx.allowed 覆盖判定，实现按用户/目录的类型策略。'},
    {'name': 'onFileUploadBefore', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '上传落盘前触发。可拦截；写 ctx.fileName 重命名、ctx.policyId 改存储策略、ctx.parentId 改归档位置。HTTP/分片/WebDAV/开放 API 共用。'},
    {'name': 'onFileUploadAfter', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '文件落盘后触发。同步可拦截：deny 时宿主回滚记录与引用，用于病毒扫描不通过即拒绝。可写 ctx.tags 自动打标签。'},
    {'name': 'onFileUploadFailed', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '上传失败（校验/落盘/被拦截）后触发。ctx={userId,fileName,size,reason}'},
    {'name': 'onFileRapidBefore', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '秒传命中前触发。可拦截以禁用特定用户/哈希的秒传。'},
    {'name': 'onFileChunkInit', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '分片上传会话创建前触发。可拦截大文件、限制分片数。'},
    {'name': 'onFileChunkComplete', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '分片合并完成后触发。mergedSize 是实际落盘字节数（非客户端声明值）。'},
    {'name': 'onFileDownloadBefore', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '登录用户下载前触发。可拦截；写 ctx.fileName 改下载名、ctx.headers 注入响应头。高频，走轻量门。'},
    {'name': 'onAntiLeech', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 1, 'doc': '【v1 兼容】登录用户下载前防盗链，error 含 "blocked" 即 403。语义不变；新插件建议用 onFileDownloadBefore。公开分享下载不走本钩子。'},
    {'name': 'onFileDownloadAfter', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '下载响应结束后触发，bytes 为实际发送字节数。用于流量计费。'},
    {'name': 'onFilePreview', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '在线预览前触发。注册型(preview)：写 ctx.redirect 接管预览，宿主 302 过去。'},
    {'name': 'onFileThumb', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '缩略图生成/命中后触发。ctx={fileId,fileName,width,height,cacheHit}'},
    {'name': 'onFileRename', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '重命名前触发。写 ctx.newName 可规范化命名（去敏感词、加前缀）。'},
    {'name': 'onFileMove', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '移动前触发。可拦截实现「禁止移出部门目录」。'},
    {'name': 'onFileCopy', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '批量复制前触发，ctx.fileIds 为 ID 列表。'},
    {'name': 'onFileDelete', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '删除（移入回收站）前触发。可拦截实现合规保留期锁定。'},
    {'name': 'onFileRestore', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '从回收站恢复后触发。'},
    {'name': 'onFilePurge', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '彻底删除（Purge/回收站自动清理）前触发。可拦截实现法务保全。'},
    {'name': 'onFileVersionCreate', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '新版本归档后触发。ctx={userId,fileId,version,size}'},
    {'name': 'onFileVersionRestore', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '恢复历史版本前触发，可拦截实现版本审批。'},
    {'name': 'onFileTagChange', 'group': 'file', 'groupLabel': '文件管理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '标签增删改后触发，action=add|remove|update。'},
    {'name': 'onContentScan', 'group': 'content', 'groupLabel': '内容治理', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '注册型(contentScan)：上传完成后、对外可见前触发。写 ctx.verdict="reject" 即拦截并回滚；可写 ctx.labels/ctx.tags 回填分类。'},
    {'name': 'onContentHashCheck', 'group': 'content', 'groupLabel': '内容治理', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '计算完哈希后触发。可拦截实现违规文件哈希黑名单。'},
    {'name': 'onContentReport', 'group': 'content', 'groupLabel': '内容治理', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '用户提交举报（POST /api/reports）后触发。宿主只落库不做判定。'},
    {'name': 'onShareCreateBefore', 'group': 'share', 'groupLabel': '分享', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '创建分享前触发。可拦截；改写 ctx.expireAt/ctx.extractCode 强制站点级分享策略。'},
    {'name': 'onShareCreateAfter', 'group': 'share', 'groupLabel': '分享', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '分享创建成功后触发，ctx.url 为完整分享链接。'},
    {'name': 'onShareAccess', 'group': 'share', 'groupLabel': '分享', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '访问公开分享页时触发（未认证）。可拦截实现地区限制、风控。'},
    {'name': 'onShareVerify', 'group': 'share', 'groupLabel': '分享', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '校验提取码/密码时触发。写 ctx.ok 放行自定义凭证或额外拒绝。ctx 不含明文密码。'},
    {'name': 'onShareDownloadBefore', 'group': 'share', 'groupLabel': '分享', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '公开分享下载/预览前触发，补上 v1 onAntiLeech 覆盖不到的公开分享盲区（防盗链/限速/次数）。'},
    {'name': 'onShareDelete', 'group': 'share', 'groupLabel': '分享', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '分享删除后触发。'},
    {'name': 'onShareExpire', 'group': 'share', 'groupLabel': '分享', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '定时心跳检测到分享过期时触发。'},
    {'name': 'onTaskCreateBefore', 'group': 'task', 'groupLabel': '离线下载', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '离线下载任务创建前触发。可拦截（URL 黑名单/BT 禁用）；写 ctx.url 做镜像改写。宿主出站 IP 校验在其后执行。'},
    {'name': 'onTaskCreateAfter', 'group': 'task', 'groupLabel': '离线下载', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '任务入库后触发。'},
    {'name': 'onTaskComplete', 'group': 'task', 'groupLabel': '离线下载', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '离线下载完成并归档为文件后触发。'},
    {'name': 'onTaskFailed', 'group': 'task', 'groupLabel': '离线下载', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '任务失败后触发。ctx={userId,taskId,reason}'},
    {'name': 'onTaskCancel', 'group': 'task', 'groupLabel': '离线下载', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '任务取消后触发。'},
    {'name': 'onWebDAVRequest', 'group': 'webdav', 'groupLabel': 'WebDAV', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '每个 WebDAV 方法进入前触发。可拦截实现方法级白名单、只读模式。'},
    {'name': 'onWebDAVPropfind', 'group': 'webdav', 'groupLabel': 'WebDAV', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': 'PROPFIND 组装响应前触发。写 ctx.props 注入自定义 DAV 属性。'},
    {'name': 'onWebDAVPut', 'group': 'webdav', 'groupLabel': 'WebDAV', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': 'WebDAV PUT 写入前触发（协议层，可见原始 DAV 路径）。'},
    {'name': 'onWebDAVDelete', 'group': 'webdav', 'groupLabel': 'WebDAV', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': 'WebDAV DELETE 前触发。'},
    {'name': 'onWebDAVLock', 'group': 'webdav', 'groupLabel': 'WebDAV', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': 'LOCK/UNLOCK 前触发，operation=lock|unlock。可拦截实现外置锁协调。'},
    {'name': 'onStorageResolve', 'group': 'storage', 'groupLabel': '存储与策略', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '注册型(storage)：filesystem.New 遇到未知策略类型时触发。写 ctx.handled=true + ctx.plugin 让宿主改走代理读写。'},
    {'name': 'onStorageBeforeWrite', 'group': 'storage', 'groupLabel': '存储与策略', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '物理对象写入前触发。可拦截实现写入审计、加密前置。'},
    {'name': 'onStorageAfterWrite', 'group': 'storage', 'groupLabel': '存储与策略', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '物理对象写入后触发。用于跨区复制、WORM 归档。'},
    {'name': 'onStorageBeforeRead', 'group': 'storage', 'groupLabel': '存储与策略', 'mode': 'sync', 'blockable': True, 'mutatable': False, 'since': 2, 'doc': '物理对象读取前触发。可拦截实现冷存储解冻、按用户限流。'},
    {'name': 'onStoragePolicyChange', 'group': 'storage', 'groupLabel': '存储与策略', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '存储策略增删改后触发，action=create|update|delete。'},
    {'name': 'onCollabOpen', 'group': 'collab', 'groupLabel': '协作编辑', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 1, 'doc': '【v1 兼容】打开协作编辑时触发。写 ctx.url 则 302 跳转（OnlyOffice 等）。v2 语义不变。'},
    {'name': 'onCollabSave', 'group': 'collab', 'groupLabel': '协作编辑', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 1, 'doc': '【v1 兼容】保存协作内容时触发，仅通知。content 是完整明文，大文件占内存。'},
    {'name': 'onCollabConnect', 'group': 'collab', 'groupLabel': '协作编辑', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '协作 WebSocket 建立连接后触发。'},
    {'name': 'onCollabDisconnect', 'group': 'collab', 'groupLabel': '协作编辑', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '协作 WebSocket 断开后触发。'},
    {'name': 'onEmail', 'group': 'notify', 'groupLabel': '通知与邮件', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 1, 'doc': '【v1 兼容】创建站内通知时触发，邮箱为空则不触发。系统不含发信实现，此钩子是邮件外发入口之一。'},
    {'name': 'onNotificationSend', 'group': 'notify', 'groupLabel': '通知与邮件', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '注册型(notify)：站内通知落库前触发。写 ctx.channel 切渠道、ctx.delivered=true 表示已由插件投递。'},
    {'name': 'onNotificationRead', 'group': 'notify', 'groupLabel': '通知与邮件', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '通知标记已读后触发。'},
    {'name': 'onCodeRedeemBefore', 'group': 'billing', 'groupLabel': '套餐与付费', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '兑换码核销前触发。可拦截；写 ctx.planId 改写实际到账套餐。'},
    {'name': 'onCodeRedeemAfter', 'group': 'billing', 'groupLabel': '套餐与付费', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '兑换成功后触发。'},
    {'name': 'onPlanSubscribe', 'group': 'billing', 'groupLabel': '套餐与付费', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '用户订阅生效后触发，source=redeem|payment|admin。'},
    {'name': 'onPlanExpire', 'group': 'billing', 'groupLabel': '套餐与付费', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '定时心跳检测到订阅过期时触发。'},
    {'name': 'onPaymentCallback', 'group': 'billing', 'groupLabel': '套餐与付费', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '注册型(payment)：POST /api/pay/callback/:provider 命中时触发。插件验签并写 ctx.status/userId/planId，宿主据此开通。'},
    {'name': 'onAuditLog', 'group': 'audit', 'groupLabel': '审计与统计', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '写操作（POST/PUT/DELETE /api）完成后触发。用于审计外送、SIEM 对接。'},
    {'name': 'onStatsCollect', 'group': 'audit', 'groupLabel': '审计与统计', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '后台统计取数时触发。插件可写 ctx.traffic/fileTypes/activity 提供数据 —— 这是让三个图表真正有数据的实现方式。'},
    {'name': 'onDashboardQuery', 'group': 'audit', 'groupLabel': '审计与统计', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '后台 KPI 查询时触发。写 ctx.cards 追加自定义指标卡片。'},
    {'name': 'onBackup', 'group': 'backup', 'groupLabel': '备份与恢复', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 1, 'doc': 'POST /api/admin/backup 触发。注册型(backup)：插件导出到 ctx.target 并写 ctx.result。宿主不做备份实现，只鉴权+审计。'},
    {'name': 'onRestore', 'group': 'backup', 'groupLabel': '备份与恢复', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 1, 'doc': 'POST /api/admin/restore 触发。插件从 ctx.source 恢复并写 ctx.result。dryRun=true 只校验不写入。需二次确认。'},
    {'name': 'onApiAuth', 'group': 'api', 'groupLabel': '开放 API 与 CLI', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 1, 'doc': 'middleware.APIAuth 校验 PAT/OAuth token 后触发。可拦截实现细粒度授权；写 ctx.claims 注入身份信息。'},
    {'name': 'onCLI', 'group': 'api', 'groupLabel': '开放 API 与 CLI', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 1, 'doc': 'nebula hook <name> [args...] 触发。注册型(cli)：插件把结果写进 ctx.output，宿主打印到 stdout。'},
    {'name': 'onPluginInstall', 'group': 'plugin', 'groupLabel': '插件生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '插件安装成功后触发（此时尚未启用）。'},
    {'name': 'onPluginEnable', 'group': 'plugin', 'groupLabel': '插件生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '插件启用（子进程拉起成功）后触发。'},
    {'name': 'onPluginDisable', 'group': 'plugin', 'groupLabel': '插件生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '插件禁用后触发。'},
    {'name': 'onPluginConfigChange', 'group': 'plugin', 'groupLabel': '插件生命周期', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '插件配置更新后触发。插件需自行重启才会读到新配置。'},
    {'name': 'onUIPageOpen', 'group': 'ui', 'groupLabel': 'UI 扩展点', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '用户打开插件注册页面（iframe 加载）时触发。ctx={plugin,page,userId,userName,ip}'},
    {'name': 'onUIThemeApply', 'group': 'ui', 'groupLabel': 'UI 扩展点', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '前端应用插件主题时触发。写 ctx.variables 可动态覆盖 CSS 变量。'},
    {'name': 'onUISettingsSave', 'group': 'ui', 'groupLabel': 'UI 扩展点', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '插件设置面板保存时触发。可拦截校验；写 ctx.values 覆盖落库值。'},
    {'name': 'onUIPageRegister', 'group': 'ui', 'groupLabel': 'UI 扩展点', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '宿主聚合 /api/plugins/ui 时触发。插件可写 ctx.sidebar/ctx.theme/ctx.settings 动态注入 UI 点。'},
    {'name': 'onAIRequest', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': 'AI 网关向模型发起请求前触发。可拦截（决策 deny 即本次不外发）；可写 ctx.model/ctx.prompt 改写请求。ctx={userId,scene,provider,model,promptChars,codeSpace}'},
    {'name': 'onAIResponse', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': 'AI 请求结束后触发（成功或失败都发）。ctx={userId,scene,provider,model,success,latencyMs,promptTokens,completionTokens,error}'},
    {'name': 'onAIProviderResolve', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '注册型(ai)：宿主点名调用 AI 能力提供方时触发。插件写 ctx.handled=true 并写 ctx.result 即可完全接管模型调用。ctx={userId,scene,prompt,maxTokens}'},
    {'name': 'onAIComplete', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '代码空间请求补全时触发（AI 网关之前）。插件写 ctx.completion 可直接提供结果，宿主不再调模型。ctx={userId,fileId,fileName,language,prefix,suffix,maxTokens}'},
    {'name': 'onAIRecommend', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '搜索联想/近期常用/热门推荐生成时触发。插件可写 ctx.suggestions 或 ctx.fileIds 覆盖内置结果。ctx={userId,q,limit}'},
    {'name': 'onAISearch', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': '自然语言搜文件时触发。插件可写 ctx.filters 覆盖解析出的检索条件、写 ctx.answer 直接作答；可拦截。ctx={userId,query,scope}'},
    {'name': 'onAISummary', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': False, 'mutatable': True, 'since': 2, 'doc': '生成文件摘要与自动标签时触发。插件可写 ctx.summary / ctx.tags 接管。ctx={userId,fileId,fileName,mimeType,size,textChars}'},
    {'name': 'onAIAsk', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'sync', 'blockable': True, 'mutatable': True, 'since': 2, 'doc': 'AI 助手问答时触发。插件可写 ctx.answer 直接回答；可拦截。ctx={userId,question,historyChars}'},
    {'name': 'onAIConfigChange', 'group': 'ai', 'groupLabel': 'AI 智能', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 2, 'doc': '后台保存 AI 配置后触发。ctx={operatorId,operatorName,enabled,provider,model,changed}'},
    {'name': 'onRateLimit', 'group': 'limit', 'groupLabel': '限流', 'mode': 'async', 'blockable': False, 'mutatable': False, 'since': 1, 'doc': '【v1 兼容】高频操作前触发。整体异步仅通知；限流中间件这一处通过 RateLimitGate 强制同步读取拦截决策。'}
]
HOOK_NAMES = [h["name"] for h in HOOKS]
HOOK_BY_NAME = {h["name"]: h for h in HOOKS}
HOOK_GROUPS = []
for _h in HOOKS:
    _g = next((g for g in HOOK_GROUPS if g["key"] == _h["group"]), None)
    if _g is None:
        _g = {"key": _h["group"], "label": _h["groupLabel"], "items": []}
        HOOK_GROUPS.append(_g)
    _g["items"].append(_h)

RUNTIMES = {
    "php":      {"label": "PHP",       "requires": "php>=7.4", "home": "in.php",     "entry": "",         "cmd": "php -S {host}:{port} -t {static}"},
    "python":   {"label": "Python",    "requires": "python3",  "home": "index.html", "entry": "server.py", "cmd": "python3 {entry}"},
    "node":     {"label": "Node.js",   "requires": "node>=18", "home": "index.html", "entry": "server.js", "cmd": "node {entry}"},
    "go":       {"label": "Go",        "requires": "go",       "home": "index.html", "entry": "main.go",   "cmd": "go run {entry}"},
    "binary":   {"label": "二进制",     "requires": "",        "home": "index.html", "entry": "plugin-bin", "cmd": "{entry}"},
    "external": {"label": "外部页面",   "requires": "",        "home": "",           "entry": "",         "cmd": ""},
}
PROVIDER_KINDS = ["storage", "auth", "notify", "preview", "payment", "contentScan",
                  "backup", "restore", "cli", "search", "theme", "page", "ai"]
PROVIDER_DEFAULT_HOOK = {
    "storage": "onStorageResolve", "auth": "onAuthProviderResolve", "notify": "onNotificationSend",
    "preview": "onFilePreview", "payment": "onPaymentCallback", "contentScan": "onContentScan",
    "backup": "onBackup", "restore": "onRestore", "cli": "onCLI", "search": "onStatsCollect",
    "theme": "onUIThemeApply", "page": "onUIPageOpen",
    "ai": "onAIProviderResolve",
}
ICONS = ["sparkles", "layout-dashboard", "puzzle", "settings", "chart-no-axes-combined",
         "shield", "bell", "database", "file-text", "globe", "image", "users",
         "hard-drive", "star", "zap", "heart", "rocket", "code", "palette", "table"]
AREAS = ["main", "admin", "public"]
ROLES = ["any", "admin", "user"]
EMBEDS = ["iframe", "newtab"]
SETTING_TYPES = ["text", "password", "number", "bool", "select", "textarea"]

# 结构控件（决定插件能做什么）
STRUCT_CONTROLS = [
    ("page",       "页面",       "PageDecl",   "侧边栏可打开的插件页面"),
    ("sidebar",    "侧边栏项",   "override",   "覆盖某页面的标题/图标/排序"),
    ("setting",    "设置字段",   "Setting",    "插件设置面板里的一个表单项"),
    ("theme",      "主题",       "单例",       "顶栏可选的主题配色"),
    ("provider",   "注册型能力", "Provider",   "声明我能当某种后端"),
    ("hook",       "钩子订阅",   "108 个",      "打开选择器批量勾选钩子"),
    ("customfile", "自定义文件", "任意",       "额外的 PHP/JS/CSS/静态文件"),
]
# 页面控件（决定插件主页长什么样）
WIDGET_CONTROLS = [
    ("w-heading", "标题",        "大标题 + 副标题"),
    ("w-text",    "段落文本",    "一段说明文字"),
    ("w-card",    "卡片",        "带标题的内容卡片"),
    ("w-stat",    "统计数字",    "大数字指标"),
    ("w-button",  "按钮",        "操作按钮"),
    ("w-input",   "输入框",      "表单输入项"),
    ("w-table",   "表格",        "数据表格"),
    ("w-alert",   "提示条",      "警告 / 提示信息"),
    ("w-divider", "分隔线",      "水平分隔"),
    ("w-html",    "自定义 HTML", "直接写 HTML/PHP 片段"),
]
TYPE_LABEL = {
    "page": "页面", "sidebar": "侧边栏项", "setting": "设置字段", "theme": "主题",
    "provider": "注册型能力", "hookgroup": "钩子订阅", "customfile": "自定义文件",
}
TYPE_LABEL.update({k: name for k, name, desc in WIDGET_CONTROLS})

MANIFEST_KEYS = {"name", "title", "version", "author", "description", "entry", "args", "env",
                 "protocolVersion", "hooks", "permissions", "pages", "providers", "license",
                 "homepage", "minHostVersion"}
BEGG_KEYS = {"runtime", "entry", "home", "listen", "port", "requires", "command", "static",
             "env", "pages", "ui", "providers"}
PAGE_KEYS = {"name", "home", "area", "title", "icon", "role", "order", "embed", "public", "url"}

# ---------------------------------------------------------------------------
# 二、代码片段（生成插件页面 / 服务 / JSON-RPC 骨架）
# ---------------------------------------------------------------------------

PHP_STATS = """<?php
$port = getenv('NB_PLUGIN_PORT') ?: '0';
$name = getenv('NB_PLUGIN_NAME') ?: '';
$site = getenv('NB_PLUGIN_SITE_URL') ?: '';
?>
<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><title>数据统计</title>
<style>body{font-family:system-ui,sans-serif;background:#0b1020;color:#e6ecff;padding:28px}h1{color:#22d3ee}table{border-collapse:collapse;width:100%;margin-top:16px}td,th{border:1px solid #2a3560;padding:8px 12px;text-align:left}code{color:#8d9cc4}</style></head>
<body><h1>数据统计</h1><p>插件 <code><?= htmlspecialchars($name) ?></code> · 端口 <code><?= htmlspecialchars($port) ?></code></p>
<table><tr><th>指标</th><th>值</th></tr><tr><td>站点</td><td><?= htmlspecialchars($site) ?></td></tr><tr><td>协议</td><td><?= htmlspecialchars(getenv('NB_PLUGIN_PROTOCOL') ?: '') ?></td></tr></table>
</body></html>"""

THEME_CSS = """/* 插件主题样式：宿主会做注入过滤，只放行安全的 CSS 声明 */
:root{
  --nebula-primary: #3b82f6;
  --nebula-accent:  #60a5fa;
}
.plugin-card{ border-radius: 14px; }"""

PY_SERVER = """#!/usr/bin/env python3
# NebulaDrive 插件页面服务：端口由宿主通过环境变量下发。
# 宿主把 cmd.Dir 设成 co.BEGG 所在目录（BeggDir），页面文件就在这里，
# 所以直接用当前工作目录即可；不要 chdir(NB_PLUGIN_DIR)。
import os
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("NB_PLUGIN_PORT", "0"))
HOST = os.environ.get("NB_PLUGIN_HOST", "127.0.0.1")
ROOT = os.getcwd()

class Handler(SimpleHTTPRequestHandler):
    def log_message(self, fmt, *args):
        pass  # 日志走 stderr，避免污染 JSON-RPC 的 stdout

if __name__ == "__main__":
    handler = partial(Handler, directory=ROOT)
    print("[plugin] serving %s on %s:%s" % (ROOT, HOST, PORT), flush=True)
    ThreadingHTTPServer((HOST, PORT), handler).serve_forever()"""

JS_SERVER = """// NebulaDrive 插件页面服务：端口由宿主通过环境变量下发。
// cmd.Dir = co.BEGG 所在目录，直接用 process.cwd() 作为文档根。
const http = require('http'), fs = require('fs'), path = require('path');
const port = parseInt(process.env.NB_PLUGIN_PORT || '0', 10);
const host = process.env.NB_PLUGIN_HOST || '127.0.0.1';
const dir  = process.cwd();
http.createServer((req, res) => {
  let p = decodeURIComponent(req.url.split('?')[0]);
  if (p === '/') p = '/index.html';
  const f = path.join(dir, p);
  if (!f.startsWith(dir)) { res.writeHead(403); return res.end('forbidden'); }
  fs.readFile(f, (e, data) => {
    if (e) { res.writeHead(404); return res.end('not found'); }
    const ext = path.extname(f);
    const mime = {'.html':'text/html; charset=utf-8','.css':'text/css','.js':'application/javascript','.json':'application/json','.svg':'image/svg+xml'}[ext] || 'application/octet-stream';
    res.writeHead(200, {'Content-Type': mime}); res.end(data);
  });
}).listen(port, host, () => console.log('[plugin] listening on ' + host + ':' + port));"""

GO_SERVER = """// NebulaDrive 插件页面服务（Go 标准库，go run main.go 即可）。
// 端口由宿主通过 NB_PLUGIN_PORT 下发；文档根 = co.BEGG 所在目录。
package main

import (
    "net/http"
    "os"
)

func main() {
    port := os.Getenv("NB_PLUGIN_PORT")
    if port == "" { port = "0" }
    host := os.Getenv("NB_PLUGIN_HOST")
    if host == "" { host = "127.0.0.1" }
    http.Handle("/", http.FileServer(http.Dir(".")))
    http.ListenAndServe(host+":"+port, nil)
}"""

PY_RPC = """#!/usr/bin/env python3
# NebulaDrive 插件 JSON-RPC 2.0 over stdio 骨架
# 宿主每行发一个 JSON 请求，插件每行回一个 JSON 响应；日志必须走 stderr。
import sys, json

HOOKS = /*__PY_HOOKS__*/[]
NAME  = /*__PY_NAME__*/''

def log(msg):
    print('[plugin] ' + msg, file=sys.stderr, flush=True)

def handle(req):
    m = req.get('method')
    if m == 'init':
        return {'ok': True, 'name': NAME, 'version': '1.0.0', 'hooks': HOOKS}
    if m == 'ping':
        return {'ok': True}
    if m == 'shutdown':
        sys.exit(0)
    if m == 'fire':
        p = req.get('params', {})
        hook, ctx = p.get('hook'), p.get('ctx', {})
        # ===== 在这里写你的业务逻辑 =====
        # 决策型：拦截上传
        if hook == 'onFileUploadBefore' and str(ctx.get('fileName','')).endswith('.exe'):
            return {'decision': 'deny', 'reason': '不允许上传可执行文件'}
        # 变换型：改写 ctx（顶层键覆盖）
        # return {'ctxPatch': {'maxStorage': 999999}}
        # 通知型：只记日志
        log('fire ' + str(hook))
        return {}
    return {'error': 'method not found'}

def main():
    for line in sys.stdin:
        line = line.strip()
        if not line: continue
        try: req = json.loads(line)
        except Exception as e:
            log('bad json: ' + str(e)); continue
        rid = req.get('id', 0)
        try: result = handle(req)
        except Exception as e:
            sys.stdout.write(json.dumps({'jsonrpc':'2.0','id':rid,'error':{'code':-32603,'message':str(e)}})+'\\n'); sys.stdout.flush(); continue
        if rid:
            sys.stdout.write(json.dumps({'jsonrpc':'2.0','id':rid,'result':result}, ensure_ascii=False)+'\\n'); sys.stdout.flush()

if __name__ == '__main__':
    main()"""

JS_RPC = """#!/usr/bin/env node
// NebulaDrive 插件 JSON-RPC 2.0 over stdio 骨架（日志走 stderr）
const readline = require('readline');
const HOOKS = /*__JS_HOOKS__*/[];
const NAME = /*__JS_NAME__*/'';
const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const log = m => console.error('[plugin] ' + m);
function handle(req){
  const m = req.method;
  if (m === 'init') return {ok:true, name:NAME, version:'1.0.0', hooks:HOOKS};
  if (m === 'ping') return {ok:true};
  if (m === 'shutdown') process.exit(0);
  if (m === 'fire'){
    const p = req.params || {}, hook = p.hook, ctx = p.ctx || {};
    if (hook === 'onFileUploadBefore' && String(ctx.fileName||'').endsWith('.exe'))
      return {decision:'deny', reason:'不允许上传可执行文件'};
    log('fire ' + hook);
    return {};
  }
  return {error:'method not found'};
}
rl.on('line', line => {
  if (!line.trim()) return;
  let req; try { req = JSON.parse(line); } catch(e){ return log('bad json'); }
  const rid = req.id || 0;
  let result; try { result = handle(req); } catch(e){
    process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:rid,error:{code:-32603,message:String(e)}})+'\\n'); return;
  }
  if (rid) process.stdout.write(JSON.stringify({jsonrpc:'2.0',id:rid,result})+'\\n');
});"""

SNIPPETS = {"@PHP_STATS": PHP_STATS, "@THEME_CSS": THEME_CSS, "@PY_SERVER": PY_SERVER,
            "@JS_SERVER": JS_SERVER, "@GO_SERVER": GO_SERVER, "@PY_RPC": PY_RPC, "@JS_RPC": JS_RPC}

# ---------------------------------------------------------------------------
# 三、状态与模板库（20 套，≥ 15 套要求）
# ---------------------------------------------------------------------------

def blank_state():
    return {
        "meta": {"name": "my-plugin", "title": "我的插件", "version": "1.0.0",
                 "author": "you", "description": "一个 NebulaDrive 插件",
                 "license": "AGPL-3.0-only", "homepage": "", "minHostVersion": "",
                 "protocolVersion": 2, "permissions": ""},
        "runtime": {"runtime": "php", "requires": "php>=7.4", "home": "in.php",
                    "entry": "", "static": "", "listen": "0.0.0.0", "port": 0,
                    "command": "", "env": {}},
        "dirLayout": "flat",
        "pages": [], "sidebar": [], "settings": [], "theme": None,
        "providers": [], "hooks": [], "customFiles": [], "widgets": [],
        "advanced": {"jsonrpc": False, "lang": "python"},
        "codeOverride": {},
    }


def _p(name, title, home="in.php", area="main", role="any", icon="puzzle",
       embed="iframe", order=10, public=False, url=""):
    return {"name": name, "title": title, "home": home, "area": area, "role": role,
            "icon": icon, "embed": embed, "order": order, "public": public, "url": url}


def _s(key, label, typ="text", default="", options="", help=""):
    return {"key": key, "label": label, "type": typ, "default": default,
            "options": options, "help": help}


def _v(kind, name, label="", hook=""):
    return {"kind": kind, "name": name, "label": label, "hook": hook}


def _w(t, text="", sub=""):
    return {"t": t, "text": text, "sub": sub}


def _rt(**kw):
    d = dict(blank_state()["runtime"]); d.update(kw); return d


def _php(**kw):
    return _rt(runtime="php", requires="php>=7.4", home="in.php", **kw)


def _py(**kw):
    return _rt(runtime="python", requires="python3", home="index.html",
               entry="server.py", command="python3 {entry}", **kw)


def _rpc(**kw):
    # 纯钩子 / 注册型插件：没有页面服务，只有 JSON-RPC 主体
    return _rt(runtime="python", requires="python3", home="", entry="", command="", **kw)


# 每套模板 = 对 blank_state 的增量补丁。content 以 @ 开头表示引用 SNIPPETS。
TEMPLATES = [
    # ---------- A. 入门 ----------
    dict(key="blank", group="入门", name="空白最小工程",
         desc="从零开始的最小可运行插件（保留一个最小页面，开箱即可构建）", patch={
             "pages": [_p("home", "我的插件", "in.php", icon="puzzle")],
             "widgets": [_w("w-heading", "我的插件", "从左侧拖入控件开始设计")],
         }),
    dict(key="hello-php", group="入门", name="Hello 面板（PHP）",
         desc="最经典的三文件 + 一个 PHP 页面，扁平目录", patch={
             "meta": {"name": "hello", "title": "Hello 面板", "description": "最小 PHP 页面插件"},
             "pages": [_p("home", "Hello 面板", "in.php", icon="sparkles")],
             "widgets": [_w("w-heading", "Hello from plugin", "由可视化制作器生成"),
                         _w("w-card", "环境信息", "端口 / 站点由 NB_PLUGIN_* 下发")],
         }),
    dict(key="ass-demo", group="入门", name="ASS/ 子目录布局",
         desc="演示目录形式不限定：根目录 plugin.json，ASS/ 里一堆 .php + 注册型 .go", patch={
             "meta": {"name": "ass-demo", "title": "ASS 面板", "description": "PHP 嵌入页面 + 子目录布局"},
             "dirLayout": "ass",
             "pages": [_p("home", "ASS 面板", "in.php", icon="layout-dashboard"),
                       _p("stats", "数据统计", "stats.php", area="admin", role="admin",
                          icon="chart-no-axes-combined", order=20)],
             "sidebar": [{"page": "home", "title": "ASS 面板", "icon": "layout-dashboard",
                          "area": "main", "role": "any", "order": 10}],
             "settings": [_s("greeting", "问候语", "text", "Hello", help="显示在面板顶部"),
                          _s("level", "等级", "select", "1", "1,2,3")],
             "theme": {"name": "ass-blue", "label": "ASS 蓝", "css": "theme.css", "preview": "",
                       "variables": {"--nebula-primary": "#3b82f6", "--nebula-accent": "#60a5fa"}},
             "widgets": [_w("w-heading", "ASS 面板", "由可视化制作器生成"),
                         _w("w-card", "运行环境", "PHP · 端口由宿主分配"),
                         _w("w-stat", "108", "可用钩子"),
                         _w("w-alert", "插件页面鉴权由插件自己负责，宿主完全透传。"),
                         _w("w-button", "点我试试")],
             "customFiles": [{"path": "stats.php", "content": "@PHP_STATS"},
                             {"path": "theme.css", "content": "@THEME_CSS"}],
         }),

    # ---------- B. 页面与运行时 ----------
    dict(key="py-dashboard", group="页面与运行时", name="Python 数据看板",
         desc="Python 标准库起 HTTP 服务，带表格与统计控件", patch={
             "meta": {"name": "py-dashboard", "title": "数据看板", "description": "Python 标准库起 HTTP 服务"},
             "runtime": _py(),
             "pages": [_p("home", "数据看板", "index.html", area="admin", role="admin",
                          icon="chart-no-axes-combined")],
             "widgets": [_w("w-heading", "数据看板", "Python 插件示例"),
                         _w("w-stat", "1,024", "今日文件"), _w("w-table", "最近活动"),
                         _w("w-alert", "页面由 python3 server.py 提供。")],
         }),
    dict(key="node-panel", group="页面与运行时", name="Node.js 面板",
         desc="Node 起页面服务，演示环境变量读取", patch={
             "meta": {"name": "node-panel", "title": "Node 面板", "description": "Node.js 起页面服务"},
             "runtime": _rt(runtime="node", requires="node>=18", home="index.html",
                            entry="server.js", command="node {entry}"),
             "pages": [_p("home", "Node 面板", "index.html", icon="zap")],
             "widgets": [_w("w-heading", "Node 面板", "由 node server.js 提供"),
                         _w("w-card", "环境变量", "NB_PLUGIN_PORT"), _w("w-button", "刷新")],
         }),
    dict(key="go-service", group="页面与运行时", name="Go 标准库 HTTP 服务",
         desc="go run main.go 起页面服务，不需要第三方依赖", patch={
             "meta": {"name": "go-service", "title": "Go 服务", "description": "Go 标准库 net/http 提供页面"},
             "runtime": _rt(runtime="go", requires="go", home="index.html",
                            entry="main.go", command="go run {entry}"),
             "pages": [_p("home", "Go 服务", "index.html", area="admin", role="admin", icon="code")],
             "widgets": [_w("w-heading", "Go 服务", "net/http 标准库"),
                         _w("w-card", "零依赖", "go run main.go 即可"),
                         _w("w-alert", "部署机需安装 Go；缺解释器会在启用时明确报错。")],
         }),
    dict(key="static-site", group="页面与运行时", name="纯静态站点",
         desc="只有 index.html + 一份 CSS，Python 内置服务器托管", patch={
             "meta": {"name": "static-site", "title": "静态站点", "description": "纯静态 HTML 插件页面"},
             "runtime": _py(),
             "pages": [_p("home", "静态站点", "index.html", icon="globe")],
             "widgets": [_w("w-heading", "静态站点", "HTML + CSS，无后端逻辑"),
                         _w("w-divider"), _w("w-text", "改「代码」页的 index.html 即可换内容。")],
             "customFiles": [{"path": "extra.css", "content": "/* 额外静态资源 */\nbody{--nebula-primary:#22d3ee}\n"}],
         }),
    dict(key="external-page", group="页面与运行时", name="外部 HTTPS 页面",
         desc="iframe 直连外部页面，不经过宿主反代，不拉起任何进程", patch={
             "meta": {"name": "ext-page", "title": "外部页面", "description": "iframe 直连外部 HTTPS 页面"},
             "runtime": _rt(runtime="external", requires="", home="", entry="", command="",
                            listen="https://example.com/app"),
             "pages": [_p("home", "外部页面", "", icon="globe", public=True,
                          url="https://example.com/app")],
             "widgets": [],
         }),
    dict(key="admin-console", group="页面与运行时", name="后台管理控制台",
         desc="admin 区域页面 + 完整设置面板（6 种字段类型）", patch={
             "meta": {"name": "admin-console", "title": "运维控制台", "description": "后台页面与设置面板全家桶"},
             "runtime": _php(),
             "pages": [_p("home", "运维控制台", "in.php", area="admin", role="admin",
                          icon="settings", order=5)],
             "sidebar": [{"page": "home", "title": "运维控制台", "icon": "settings",
                          "area": "admin", "role": "admin", "order": 5}],
             "settings": [_s("apiToken", "API Token", "password", help="仅保存在插件侧"),
                          _s("refresh", "刷新间隔(秒)", "number", "30"),
                          _s("mode", "运行模式", "select", "normal", "safe,normal,fast"),
                          _s("verbose", "详细日志", "bool", "false"),
                          _s("note", "备注", "textarea", ""),
                          _s("title", "自定义标题", "text", "运维控制台")],
             "widgets": [_w("w-heading", "运维控制台", "后台 · 仅管理员可见"),
                         _w("w-stat", "0", "待处理告警"), _w("w-table", "任务列表")],
         }),

    # ---------- C. 主题与 UI ----------
    dict(key="ocean-theme", group="主题与 UI", name="海洋蓝主题包",
         desc="只提供主题与设置面板的轻量插件，无钩子", patch={
             "meta": {"name": "ocean-theme", "title": "海洋蓝主题", "description": "主题与设置面板示例"},
             "pages": [_p("home", "主题设置", "in.php", icon="palette")],
             "theme": {"name": "ocean", "label": "海洋蓝", "css": "theme.css", "preview": "",
                       "variables": {"--nebula-primary": "#0ea5e9", "--nebula-accent": "#38bdf8",
                                     "--nebula-bg": "#08131f"}},
             "settings": [_s("blur", "背景模糊", "bool", "true"),
                          _s("density", "界面密度", "select", "normal", "compact,normal,loose")],
             "widgets": [_w("w-heading", "海洋蓝", "主题与设置面板示例"),
                         _w("w-card", "CSS 变量覆盖", "--nebula-primary 等"),
                         _w("w-divider"), _w("w-text", "在顶栏主题选择器里切换即可生效。")],
             "customFiles": [{"path": "theme.css", "content": "@THEME_CSS"}],
         }),
    dict(key="sidebar-suite", group="主题与 UI", name="UI 全家桶",
         desc="主界面 + 后台双页面、侧边栏覆盖、主题、设置一次配齐", patch={
             "meta": {"name": "ui-suite", "title": "UI 全家桶", "description": "页面/侧边栏/主题/设置全演示"},
             "dirLayout": "ass",
             "pages": [_p("home", "我的主页", "in.php", icon="layout-dashboard", order=10),
                       _p("admin", "后台配置", "admin.php", area="admin", role="admin",
                          icon="settings", order=20)],
             "sidebar": [{"page": "home", "title": "我的主页", "icon": "star", "area": "main", "order": 1},
                         {"page": "admin", "title": "后台配置", "icon": "settings", "area": "admin", "order": 2}],
             "settings": [_s("accent", "主色", "text", "#22d3ee"),
                          _s("showTips", "显示提示", "bool", "true")],
             "theme": {"name": "suite", "label": "青蓝", "css": "theme.css", "preview": "",
                       "variables": {"--nebula-primary": "#22d3ee"}},
             "widgets": [_w("w-heading", "UI 全家桶", "页面 / 侧边栏 / 主题 / 设置"),
                         _w("w-card", "两个区域", "main + admin"),
                         _w("w-input", "输入点什么", "表单项演示")],
             "customFiles": [{"path": "admin.php", "content": "@PHP_STATS"},
                             {"path": "theme.css", "content": "@THEME_CSS"}],
         }),

    # ---------- D. 钩子（JSON-RPC） ----------
    dict(key="upload-guard", group="钩子 / JSON-RPC", name="上传守卫（决策型）",
         desc="订阅上传前后钩子，拦截 .exe，演示 decision=deny", patch={
             "meta": {"name": "upload-guard", "title": "上传守卫", "description": "订阅钩子拦截上传的 JSON-RPC 插件"},
             "runtime": _rpc(),
             "hooks": ["onBoot", "onFileUploadBefore", "onFileUploadAfter", "onShutdown"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "pages": [], "widgets": [],
         }),
    dict(key="waf-gate", group="钩子 / JSON-RPC", name="请求 WAF / 维护模式",
         desc="同步可拦截钩子：onRequestBefore / onApiAuth / onRateLimit", patch={
             "meta": {"name": "waf-gate", "title": "请求 WAF", "description": "在请求进入业务前做拦截与改写"},
             "runtime": _rpc(),
             "hooks": ["onBoot", "onRequestBefore", "onOpenAPIRequest", "onApiAuth",
                       "onRateLimit", "onHealthCheck", "onShutdown"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "pages": [], "widgets": [],
         }),
    dict(key="audit-log", group="钩子 / JSON-RPC", name="全量审计日志",
         desc="异步通知钩子：登录、文件、分享、下载全部留痕", patch={
             "meta": {"name": "audit-log", "title": "审计日志", "description": "订阅通知型钩子写审计流水"},
             "runtime": _rpc(),
             "hooks": ["onBoot", "onAuthLoginSuccess", "onAuthLoginFailed", "onFileUploadAfter",
                       "onFileDownloadAfter", "onFileDelete", "onShareCreateAfter",
                       "onTaskComplete", "onTaskFailed", "onAuditLog", "onConfigChanged",
                       "onPluginEnable", "onPluginDisable", "onShutdown"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "pages": [], "widgets": [],
         }),
    dict(key="quota-patch", group="钩子 / JSON-RPC", name="配额变换（ctxPatch）",
         desc="变换型钩子：改写 ctx 顶层键，演示 ctxPatch 用法", patch={
             "meta": {"name": "quota-patch", "title": "配额变换", "description": "用 ctxPatch 改写上传上下文"},
             "runtime": _rpc(),
             "hooks": ["onBoot", "onUserQuotaChange", "onFileUploadBefore", "onStoragePolicyChange"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "pages": [], "widgets": [],
         }),
    dict(key="node-rpc", group="钩子 / JSON-RPC", name="Node.js 钩子插件",
         desc="用 Node.js 写 JSON-RPC 主体，演示高级·骨架语言切换", patch={
             "meta": {"name": "node-rpc", "title": "Node 钩子", "description": "Node.js 版 JSON-RPC 骨架"},
             "runtime": _rt(runtime="node", requires="node>=18", home="", entry="", command=""),
             "hooks": ["onBoot", "onScheduledTick", "onStatsCollect", "onShutdown"],
             "advanced": {"jsonrpc": True, "lang": "node"},
             "pages": [], "widgets": [],
         }),

    # ---------- E. 注册型能力 ----------
    dict(key="provider-storage", group="注册型能力", name="自定义存储后端",
         desc="注册 storage 能力，由 onStorageResolve 点名调用", patch={
             "meta": {"name": "my-storage", "title": "自定义存储", "description": "注册型 storage 后端"},
             "runtime": _rpc(),
             "providers": [_v("storage", "mystore", "我的对象存储")],
             "hooks": ["onBoot", "onStorageResolve", "onStorageBeforeWrite", "onStorageAfterWrite"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "pages": [], "widgets": [],
         }),
    dict(key="provider-notify", group="注册型能力", name="自定义通知渠道",
         desc="注册 notify + preview 两种能力，不订阅钩子也能被点名", patch={
             "meta": {"name": "my-notify", "title": "通知渠道", "description": "注册型 notify / preview 能力"},
             "runtime": _py(),
             "providers": [_v("notify", "webhook", "企业微信 Webhook"),
                           _v("preview", "office", "Office 预览")],
             "pages": [_p("home", "通知渠道", "index.html", area="admin", role="admin", icon="bell")],
             "widgets": [_w("w-heading", "通知渠道", "注册型能力面板"),
                         _w("w-card", "Webhook", "onNotificationSend")],
         }),
    dict(key="backup-restore", group="注册型能力", name="备份与恢复扩展",
         desc="backup / restore / cli / search 四类注册 + 对应钩子", patch={
             "meta": {"name": "backup-pro", "title": "备份增强", "description": "备份恢复与 CLI 注册型能力"},
             "runtime": _rpc(),
             "providers": [_v("backup", "s3backup", "S3 备份"), _v("restore", "s3restore", "S3 恢复"),
                           _v("cli", "backupctl", "备份命令"), _v("search", "fulltext", "全文检索")],
             "hooks": ["onBoot", "onBackup", "onRestore", "onCLI", "onStatsCollect", "onShutdown"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "pages": [], "widgets": [],
         }),

    # ---------- F. AI 场景 ----------
    dict(key="ai-chat", group="AI 场景", name="AI 助手页面",
         desc="Python 页面服务 + API Key 设置项，附带「给 AI 的插件生成要求」模板", patch={
             "meta": {"name": "ai-chat", "title": "AI 助手", "description": "AI 对话页面（插件自负责鉴权）"},
             "runtime": _py(),
             "pages": [_p("home", "AI 助手", "index.html", icon="sparkles")],
             "settings": [_s("apiKey", "API Key", "password", help="填你自己的 Key，别写进代码"),
                          _s("model", "模型", "text", "glm-4-flash"),
                          _s("temperature", "温度", "number", "0.7"),
                          _s("stream", "流式输出", "bool", "true")],
             "widgets": [_w("w-heading", "AI 助手", "由插件页面自己调用大模型"),
                         _w("w-input", "问点什么…", "输入框"),
                         _w("w-button", "发送"),
                         _w("w-alert", "宿主不代理 AI 请求；Key 只在插件侧使用，切勿下发到前端。")],
         }),
    dict(key="ai-scan", group="AI 场景", name="AI 内容审核",
         desc="contentScan 注册型能力 + onContentScan 钩子，可做上传前审核", patch={
             "meta": {"name": "ai-scan", "title": "AI 内容审核", "description": "注册 contentScan 能力做上传审核"},
             "runtime": _rpc(),
             "providers": [_v("contentScan", "ai-moderation", "AI 审核")],
             "hooks": ["onBoot", "onContentScan", "onFileUploadBefore", "onContentReport", "onShutdown"],
             "advanced": {"jsonrpc": True, "lang": "python"},
             "settings": [_s("threshold", "拦截阈值", "number", "0.8")],
             "pages": [], "widgets": [],
         }),
]

# 上面 provider-notify 定义里曾尝试过另一种写法，这里统一清理：确保 key 唯一
_TPL_SEEN = set()
_TPL_CLEAN = []
for _t in TEMPLATES:
    if _t["key"] in _TPL_SEEN:
        continue
    _TPL_SEEN.add(_t["key"])
    _TPL_CLEAN.append(_t)
TEMPLATES = _TPL_CLEAN
TEMPLATE_BY_KEY = {t["key"]: t for t in TEMPLATES}
TEMPLATE_GROUPS = []
for _t in TEMPLATES:
    _g = next((g for g in TEMPLATE_GROUPS if g["name"] == _t["group"]), None)
    if _g is None:
        _g = {"name": _t["group"], "items": []}
        TEMPLATE_GROUPS.append(_g)
    _g["items"].append(_t)


def deep_update(dst, src):
    for k, v in src.items():
        if isinstance(v, dict) and isinstance(dst.get(k), dict):
            deep_update(dst[k], v)
        else:
            dst[k] = copy.deepcopy(v)
    return dst


def apply_template(key):
    s = blank_state()
    t = TEMPLATE_BY_KEY.get(key)
    if t:
        deep_update(s, t["patch"])
        for f in s.get("customFiles", []):
            c = f.get("content", "")
            if isinstance(c, str) and c.startswith("@") and c in SNIPPETS:
                f["content"] = SNIPPETS[c]
    return s


# ---------------------------------------------------------------------------
# 四、文件生成（严格对齐宿主 v2 契约）
# ---------------------------------------------------------------------------

def _jstr(v):
    return json.dumps(str(v or ""), ensure_ascii=False)


def _go_arr(items):
    return "[]string{" + ", ".join(_jstr(x) for x in items) + "}"


def rpc_entry_name(s):
    return "plugin.js" if s["advanced"].get("lang") == "node" else "plugin.py"


def page_decls(s):
    out = []
    for p in s["pages"]:
        o = {"name": p.get("name", ""), "area": p.get("area", "main"),
             "title": p.get("title", ""), "home": p.get("home", "")}
        for k in ("icon", "role", "embed", "order", "public", "url"):
            v = p.get(k)
            if v not in ("", None, False, 0) and v is not False:
                o[k] = v
        out.append(o)
    return out


def provider_decls(s):
    out = []
    for p in s["providers"]:
        o = {"kind": p.get("kind", ""), "name": p.get("name", "")}
        if p.get("label"):
            o["label"] = p["label"]
        hk = p.get("hook", "")
        if hk and hk != PROVIDER_DEFAULT_HOOK.get(p.get("kind")):
            o["hook"] = hk
        out.append(o)
    return out


def install_list(s):
    files = ["plugin.json", "register.go", "co.BEGG"]
    r = s["runtime"]
    for v in [r.get("home"), r.get("entry")]:
        if v:
            files.append(v)
    for p in s["pages"]:
        if p.get("home"):
            files.append(p["home"])
    for f in s["customFiles"]:
        if f.get("path"):
            files.append(f["path"])
    if s["theme"] and s["theme"].get("css"):
        files.append(s["theme"]["css"])
    if s["hooks"] or s["advanced"].get("jsonrpc"):
        files.append(rpc_entry_name(s))
    seen, out = set(), []
    for f in files:
        if f not in seen:
            seen.add(f); out.append(f)
    return out


def gen_register_go(s):
    m, r = s["meta"], s["runtime"]
    pkg = "p_" + re.sub(r"[^a-z0-9_]", "_", m["name"])
    provides = [p["kind"] + ":" + p["name"] for p in s["providers"]]
    lines = [
        "package " + pkg,
        "",
        "// register.go —— 纯声明文件：宿主只用 go/parser 读语法树，不编译、不执行。",
        "// 下面这个类型可以根本不存在；部署机不需要安装 Go。",
        "// 由 NebulaDrive 插件制作器（Python 桌面版）生成。",
        "var Spec = PluginSpec{",
        "    Name:            " + _jstr(m["name"]) + ",",
        "    Title:           " + _jstr(m["title"]) + ",",
        "    Version:         " + _jstr(m["version"]) + ",",
        "    Author:          " + _jstr(m["author"]) + ",",
        "    Description:     " + _jstr(m["description"]) + ",",
        "    ProtocolVersion: " + str(int(m["protocolVersion"])) + ",",
        "    Hooks:           " + _go_arr(s["hooks"]) + ",",
        "    Provides:        " + _go_arr(provides) + ",",
        "    Install:         " + _go_arr(install_list(s)) + ",",
        "    Runtime:         " + _jstr(r["runtime"]) + ",",
        "    Home:            " + _jstr(r["home"]) + ",",
        "}",
    ]
    return "\n".join(lines) + "\n"


def gen_plugin_json(s):
    m = s["meta"]
    o = {"name": m["name"], "title": m["title"], "version": m["version"],
         "author": m["author"], "description": m["description"],
         "protocolVersion": int(m["protocolVersion"])}
    need_entry = bool(s["hooks"]) or bool(s["advanced"].get("jsonrpc"))
    if need_entry:
        o["entry"] = rpc_entry_name(s)
    if s["hooks"]:
        o["hooks"] = list(s["hooks"])
    if s["pages"]:
        o["pages"] = page_decls(s)
    if s["providers"]:
        o["providers"] = provider_decls(s)
    if m.get("license"):
        o["license"] = m["license"]
    if m.get("homepage"):
        o["homepage"] = m["homepage"]
    if m.get("minHostVersion"):
        o["minHostVersion"] = m["minHostVersion"]
    perms = m.get("permissions", "")
    if isinstance(perms, str):
        perms = [x.strip() for x in perms.split(",") if x.strip()]
    if perms:
        o["permissions"] = perms
    return json.dumps(o, ensure_ascii=False, indent=2) + "\n"


def gen_begg(s):
    r = s["runtime"]
    o = {"runtime": r["runtime"]}
    req = r.get("requires", "")
    if isinstance(req, str):
        req = [x.strip() for x in req.split(",") if x.strip()]
    if req:
        o["requires"] = req
    for k in ("entry", "home", "static", "listen"):
        if r.get(k):
            o[k] = r[k]
    if r.get("port"):
        o["port"] = int(r["port"])
    if r.get("command"):
        o["command"] = r["command"]
    if r.get("env"):
        o["env"] = r["env"]
    if s["pages"]:
        o["pages"] = page_decls(s)
    ui = {}
    if s["sidebar"]:
        ui["sidebar"] = []
        for x in s["sidebar"]:
            q = {"page": x.get("page", "")}
            for k in ("title", "icon", "area", "role", "order"):
                if x.get(k) not in ("", None, 0):
                    q[k] = x[k]
            ui["sidebar"].append(q)
    if s["theme"]:
        t = {"name": s["theme"].get("name", "")}
        for k in ("label", "css", "preview"):
            if s["theme"].get(k):
                t[k] = s["theme"][k]
        if s["theme"].get("variables"):
            t["variables"] = s["theme"]["variables"]
        ui["theme"] = t
    if s["settings"]:
        ui["settings"] = []
        for x in s["settings"]:
            q = {"key": x.get("key", ""), "label": x.get("label", ""), "type": x.get("type", "text")}
            for k in ("default", "options", "help"):
                if x.get(k):
                    q[k] = x[k]
            ui["settings"].append(q)
    if ui:
        o["ui"] = ui
    if s["providers"]:
        o["providers"] = provider_decls(s)
    return json.dumps(o, ensure_ascii=False, indent=2) + "\n"


def _esc(v):
    return (str(v if v is not None else "")
            .replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;")
            .replace('"', "&quot;").replace("'", "&#39;"))


def widget_html(w):
    t, sub = w.get("text", ""), w.get("sub", "")
    k = w.get("t")
    if k == "w-heading":
        return '<div class="widget"><h2 style="font-size:20px">%s</h2>%s</div>' % (
            _esc(t), '<div style="color:var(--muted);font-size:12px">%s</div>' % _esc(sub) if sub else "")
    if k == "w-text":
        return '<div class="widget" style="font-size:13px;color:#c8d4f5">%s</div>' % _esc(t)
    if k == "w-card":
        return ('<div class="widget pv-card"><b style="font-size:13px">%s</b>'
                '<div style="color:var(--muted);font-size:12px;margin-top:5px">%s</div></div>') % (_esc(t), _esc(sub))
    if k == "w-stat":
        return '<div class="widget"><div class="pv-stat">%s</div><div style="color:var(--muted);font-size:11.5px">%s</div></div>' % (_esc(t), _esc(sub))
    if k == "w-button":
        return '<div class="widget"><span class="pv-btn">%s</span></div>' % _esc(t)
    if k == "w-input":
        return ('<div class="widget"><label style="display:block;font-size:11.5px;color:var(--muted);margin-bottom:4px">%s</label>'
                '<input class="pv-input" placeholder="%s"></div>') % (_esc(sub or t), _esc(t))
    if k == "w-table":
        return ('<div class="widget"><b style="font-size:12.5px">%s</b><table class="pv-table" style="margin-top:8px">'
                '<tr><th>项目</th><th>值</th><th>状态</th></tr>'
                '<tr><td>示例 A</td><td>128</td><td>正常</td></tr>'
                '<tr><td>示例 B</td><td>64</td><td>正常</td></tr></table></div>') % _esc(t)
    if k == "w-alert":
        return '<div class="widget pv-alert">%s</div>' % _esc(t)
    if k == "w-divider":
        return '<div class="pv-divider"></div>'
    if k == "w-html":
        return '<div class="widget">%s</div>' % t
    return ""


PAGE_CSS = """  :root{--nebula-primary:#22d3ee;--nebula-accent:#0ea5e9;--line:#2a3560;--muted:#8d9cc4}
  *{box-sizing:border-box}
  body{margin:0;padding:22px 24px;background:#0f1630;color:#e6ecff;font:14px/1.6 -apple-system,BlinkMacSystemFont,'Segoe UI','Microsoft YaHei',sans-serif}
  h1,h2,h3{margin:0 0 8px}
  .widget{margin-bottom:12px}
  .pv-card{border:1px solid var(--line);border-radius:12px;padding:14px;background:#141d3c}
  .pv-stat{font-size:26px;font-weight:700;color:var(--nebula-primary)}
  .pv-btn{display:inline-block;padding:8px 16px;border-radius:9px;background:linear-gradient(135deg,var(--nebula-accent),#6366f1);color:#fff;font-size:12.5px;cursor:pointer;border:none}
  .pv-input{display:block;width:100%;max-width:300px;background:#0b1020;border:1px solid var(--line);border-radius:8px;padding:8px 11px;color:#e6ecff;font-size:12.5px}
  .pv-table{width:100%;border-collapse:collapse;font-size:12.5px}
  .pv-table td,.pv-table th{border:1px solid var(--line);padding:7px 10px;text-align:left}
  .pv-table th{color:var(--muted);background:#141d3c}
  .pv-alert{padding:10px 14px;border-radius:9px;background:#fbbf2415;border:1px solid #fbbf2455;color:#fbbf24;font-size:12.5px}
  .pv-divider{height:1px;background:var(--line);margin:14px 0}"""


def gen_page_html(s):
    body = "\n      ".join(widget_html(w) for w in s["widgets"])
    note = ""
    if s["settings"]:
        note = ('<p style="color:var(--muted);font-size:11.5px;margin-top:18px">插件设置项：%s</p>'
                % _esc(" / ".join(x.get("label", "") for x in s["settings"])))
    return """<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>%s</title>
<style>
%s
</style>
</head>
<body>
      %s
      %s
</body>
</html>""" % (_esc(s["meta"]["title"] or s["meta"]["name"]), PAGE_CSS, body, note)


def gen_page_php(s):
    return """<?php
// 由 NebulaDrive 插件制作器（Python 桌面版）生成。宿主下发的环境变量：
//   NB_PLUGIN_NAME / NB_PLUGIN_DIR / NB_PLUGIN_HOME / NB_PLUGIN_SITE_URL
//   NB_PLUGIN_PORT / NB_PLUGIN_HOST / NB_PLUGIN_PROTOCOL
// 注意：宿主对 /ext/* 完全透传鉴权，请在本文件里自己做权限校验。
$plugin = getenv('NB_PLUGIN_NAME') ?: %s;
$site   = getenv('NB_PLUGIN_SITE_URL') ?: '';
?>
%s""" % (json.dumps(s["meta"]["name"], ensure_ascii=False), gen_page_html(s))


def gen_rpc_file(s):
    if s["advanced"].get("lang") == "node":
        return (JS_RPC.replace("/*__JS_HOOKS__*/[]", json.dumps(s["hooks"]))
                      .replace("/*__JS_NAME__*/''", json.dumps(s["meta"]["name"], ensure_ascii=False)))
    return (PY_RPC.replace("/*__PY_HOOKS__*/[]", json.dumps(s["hooks"]))
                  .replace("/*__PY_NAME__*/''", json.dumps(s["meta"]["name"], ensure_ascii=False)))


def gen_readme(s, sub):
    m, r = s["meta"], s["runtime"]
    txt = "# %s\n\n%s\n\n" % (m["title"] or m["name"], m["description"])
    txt += "- 插件名：`%s`\n- 版本：`%s`\n- 协议：v%s\n- 运行时：`%s`\n" % (
        m["name"], m["version"], m["protocolVersion"], r["runtime"])
    txt += "- 目录布局：%s\n\n" % (
        "`plugin.json` / `README.md` 在插件根目录；`register.go` / `co.BEGG` / 页面文件在 `%s` 子目录（宿主递归定位，深度 ≤ 5）" % sub
        if sub else "扁平（三文件与页面都在插件根目录）")
    txt += ("## 安装\n\n1. 把整个目录解压到 `plugins/%s/`。\n"
            "2. 后台 → 插件 → 开启**全局总开关**（需勾选风险确认）。\n"
            "3. 启用 `%s`。\n\n> 插件默认关闭，这是刻意设计。\n\n") % (m["name"], m["name"])
    if s["hooks"]:
        txt += ("## 钩子\n\n订阅：%s\n\n有钩子订阅时宿主会拉起 `%s` 做 JSON-RPC over stdio。\n"
                "**部署注意**：`entry` 相对插件安装**根目录**解析，宿主用 `exec` 直接执行它。\n"
                "Linux/macOS 需保留 shebang 并 `chmod +x %s`；Windows 请改成 `plugin.exe`\n"
                "并同步修改 `plugin.json` 的 `entry`。\n\n") % (
            "、".join("`%s`" % h for h in s["hooks"]), rpc_entry_name(s), rpc_entry_name(s))
    txt += "## 页面\n\n" + (
        "\n".join("- `%s`（%s）→ %s %s" % (
            p["name"], p["area"], p.get("url") or p.get("home") or "/",
            "· public（宿主不鉴权）" if p.get("public") else "") for p in s["pages"])
        if s["pages"] else "（无页面）") + "\n\n"
    txt += ("> `home` 一律相对 **co.BEGG 所在目录**，不要写成 `%sin.php` 这种带子目录前缀的路径。\n\n"
            % sub)
    txt += ("## 安全提醒\n\n宿主对 `/ext/*` **完全透传鉴权**：不校验登录态、不注入 token、不设 iframe sandbox。\n"
            "插件页面必须自己做权限校验。开启插件等同于把服务器执行权限交给插件作者。\n")
    return txt


def build_files(s):
    """返回 {相对路径: 文本内容}，路径用 / 分隔。"""
    f = {}
    sub = "ASS/" if s["dirLayout"] == "ass" else ""
    r = s["runtime"]
    f["plugin.json"] = gen_plugin_json(s)
    f[sub + "register.go"] = gen_register_go(s)
    f[sub + "co.BEGG"] = gen_begg(s)

    def page_content(home):
        return gen_page_php(s) if home.lower().endswith(".php") else gen_page_html(s)

    if r.get("home") and r["runtime"] != "external":
        target = sub + r["home"]
        if target not in f:
            f[target] = page_content(r["home"])
    for p in s["pages"]:
        if p.get("home") and not p.get("url"):
            target = sub + p["home"]
            if target not in f:
                f[target] = page_content(p["home"])
    if r.get("entry"):
        target = sub + r["entry"]
        if target not in f:
            if r["runtime"] == "node":
                f[target] = JS_SERVER
            elif r["runtime"] == "go":
                f[target] = GO_SERVER
            else:
                f[target] = PY_SERVER
    for c in s["customFiles"]:
        if c.get("path"):
            f[sub + c["path"]] = c.get("content", "")
    if s["theme"] and s["theme"].get("css"):
        target = sub + s["theme"]["css"]
        if target not in f:
            f[target] = THEME_CSS
    if s["hooks"] or s["advanced"].get("jsonrpc"):
        f[rpc_entry_name(s)] = gen_rpc_file(s)
    f["README.md"] = gen_readme(s, sub)
    return f


# ---------------------------------------------------------------------------
# 五、校验（模拟宿主 ParseManifest / ParseBegg / crossCheck / ValidateFiles）
# ---------------------------------------------------------------------------

def validate(s):
    res = []
    def add(ok, name, detail, warn=False):
        res.append({"ok": bool(ok), "name": name, "detail": detail, "warn": bool(warn)})

    m, r = s["meta"], s["runtime"]
    name = m.get("name", "")
    add(bool(name) and 2 <= len(name) <= 64 and re.fullmatch(r"[a-z0-9_-]+", name or "")
        and not name.startswith(("-", "_")),
        "plugin.json · name", "2–64 字符，仅 [a-z0-9-_]，不能以 -/_ 开头。当前：" + (name or "(空)"))
    ver = m.get("version", "")
    add(not ver or bool(re.fullmatch(r"\d+\.\d+\.\d+([-+][0-9A-Za-z.\-]+)?", ver)),
        "plugin.json · version", "语义化 x.y.z；可留空。当前：" + (ver or "(空)"))
    add(int(m.get("protocolVersion", 0)) in (1, 2),
        "plugin.json · protocolVersion", "宿主支持 1~2。当前：%s" % m.get("protocolVersion"))
    add(bool(s["hooks"] or s["pages"] or s["providers"]), "三者不可皆空",
        "hooks / pages / providers 至少声明一个，否则插件什么都不做。")
    add(len(s["hooks"]) <= 64, "hooks 数量上限", "当前 %d 个，上限 64。" % len(s["hooks"]))
    bad = [h for h in s["hooks"] if h not in HOOK_NAMES]
    add(not bad, "hooks 合法性",
        ("未知钩子：" + ", ".join(bad)) if bad else "%d 个钩子全部在宿主 %d 个钩子表内。" % (len(s["hooks"]), len(HOOKS)))
    add(len(set(s["hooks"])) == len(s["hooks"]), "hooks 不重复", "重复声明会被 ParseManifest 拒绝。")
    badp = [p for p in s["pages"] if not re.fullmatch(r"[a-z0-9_-]+", p.get("name", "") or "")
            or p.get("area") not in AREAS]
    add(not badp, "pages 合法性",
        ("name/area 非法：" + ", ".join(p.get("name", "?") for p in badp)) if badp
        else "%d 个页面，name/area 合法。" % len(s["pages"]))
    add(all(p.get("role", "") in ("",) or p.get("role") in ROLES for p in s["pages"])
        and all(p.get("embed", "") in ("",) or p.get("embed") in EMBEDS for p in s["pages"]),
        "pages · role / embed", "role 只能是 admin|user|any；embed 只能是 iframe|newtab。")
    badprov = [p for p in s["providers"] if p.get("kind") not in PROVIDER_KINDS or not p.get("name")]
    add(not badprov, "providers 合法性",
        ("kind/name 非法：" + json.dumps(badprov, ensure_ascii=False)) if badprov
        else "%d 个注册型能力，kind 均合法。" % len(s["providers"]))
    add(all(not p.get("hook") or p["hook"] in HOOK_NAMES for p in s["providers"]),
        "providers · hook", "若显式覆盖 hook，必须是钩子表内登记的名字。")
    add(r["runtime"] in RUNTIMES, "co.BEGG · runtime",
        "可选 php|python|node|go|binary|external。当前：" + r["runtime"])
    try:
        port = int(r.get("port", 0))
    except (TypeError, ValueError):
        port = -1
    add(0 <= port <= 65535, "co.BEGG · port", "0 表示宿主动态分配；范围 0–65535。当前：%s" % r.get("port"))
    reqs = r.get("requires", "")
    if isinstance(reqs, str):
        reqs = [x.strip() for x in reqs.split(",") if x.strip()]
    req_re = re.compile(r"^[a-zA-Z0-9_.-]+\s*(>=|>|==)?\s*([0-9][0-9.]*)?$")
    badreq = [x for x in reqs if not req_re.match(x)]
    add(not badreq, "co.BEGG · requires",
        "格式示例 php>=8.1 / python3 / node>=18。" + (" 非法：" + ", ".join(badreq) if badreq else ""))
    paths = [x for x in (r.get("home"), r.get("static")) if x]
    badpath = [x for x in paths if x.startswith("/") or ".." in x]
    add(not badpath, "co.BEGG · 路径不越界",
        "home/static 必须是 co.BEGG 所在目录内的相对路径。" + (" 问题：" + ", ".join(badpath) if badpath else ""))

    files = final_files(s)
    sub = "ASS/" if s["dirLayout"] == "ass" else ""
    homes = []
    for x in [r.get("home")] + [p.get("home") for p in s["pages"]]:
        if x and x not in homes:
            homes.append(x)
    missing = [h for h in homes if sub + h not in files]
    add(not missing, "home 文件真实存在",
        ("缺失：" + ", ".join(missing)) if missing else "已生成 %d 个页面文件，ValidateFiles 通过。" % len(homes))
    need_entry = bool(s["hooks"]) or bool(s["advanced"].get("jsonrpc"))
    add(not need_entry or rpc_entry_name(s) in files, "plugin.json · entry",
        ("已生成 " + rpc_entry_name(s)) if need_entry else "纯页面插件可省略 entry。")
    add(r["runtime"] != "external" or any(p.get("url") for p in s["pages"]),
        "external 页面", "runtime=external 时必须在 pages 里声明带 url 的页面。")

    # 模拟 crossCheck：register.go 与 plugin.json 的 Hooks/Name/Runtime/Home
    go = files.get(sub + "register.go", "")
    decl_hooks = [x[1:-1] for x in re.findall(r'"[^"]*"', (re.search(r"Hooks:\s*\[\]string\{([^}]*)\}", go) or [None, ""])[1])]
    decl_name = (re.search(r'Name:\s*"([^"]*)"', go) or [None, ""])[1]
    decl_rt = (re.search(r'Runtime:\s*"([^"]*)"', go) or [None, ""])[1]
    decl_home = (re.search(r'Home:\s*"([^"]*)"', go) or [None, ""])[1]
    try:
        man = json.loads(files["plugin.json"])
        man_hooks = man.get("hooks", [])
        man_err = ""
    except Exception as e:
        man, man_hooks, man_err = {}, ["!PARSE"], str(e)
    add(sorted(decl_hooks) == sorted(man_hooks) and "!PARSE" not in man_hooks,
        "crossCheck · Hooks 一致", "register.go 与 plugin.json 的钩子集合必须完全一致。" + (" " + man_err if man_err else ""))
    add(decl_name == m["name"], "crossCheck · Name 一致",
        "register.go Name=%s，plugin.json name=%s" % (decl_name or "(空)", m["name"]))
    add(not decl_rt or decl_rt == r["runtime"], "crossCheck · Runtime 一致",
        "register.go Runtime=%s，co.BEGG runtime=%s" % (decl_rt, r["runtime"]))
    add(not decl_home or not r.get("home") or decl_home == r["home"], "crossCheck · Home 一致",
        "register.go Home=%s，co.BEGG home=%s" % (decl_home or "(空)", r.get("home") or "(空)"))

    json_ok, json_msg = True, "两个 JSON 文件均可解析，字段均在宿主白名单内。"
    try:
        mj = json.loads(files["plugin.json"])
        unknown = set(mj) - MANIFEST_KEYS
        if unknown:
            json_ok, json_msg = False, "plugin.json 含宿主不认识的字段：" + ", ".join(sorted(unknown))
        elif not mj.get("name"):
            json_ok, json_msg = False, "plugin.json 缺 name"
    except Exception as e:
        json_ok, json_msg = False, "plugin.json 解析失败：" + str(e)
    if json_ok:
        try:
            bj = json.loads(files[sub + "co.BEGG"])
            unknown = set(bj) - BEGG_KEYS
            if unknown:
                json_ok, json_msg = False, "co.BEGG 含宿主不认识的字段：" + ", ".join(sorted(unknown))
        except Exception as e:
            json_ok, json_msg = False, "co.BEGG 解析失败：" + str(e)
    add(json_ok, "JSON 语法与字段白名单", json_msg)

    ov = [k for k, v in (s.get("codeOverride") or {}).items() if v is not None]
    if ov:
        add(True, "手工代码覆盖",
            "以下文件由「代码」页手工编辑，不参与结构同步：%s。请自行确认其合法性。" % ", ".join(ov), warn=True)
    return res


def final_files(s):
    f = build_files(s)
    for k, v in (s.get("codeOverride") or {}).items():
        if v is not None:
            f[k] = v
    return f


def is_exec(path):
    return path.endswith((".py", ".js", ".sh")) or path == "plugin-bin"


def make_zip(s, out_path):
    f = final_files(s)
    root = s["meta"]["name"] or "plugin"
    with zipfile.ZipFile(out_path, "w", zipfile.ZIP_STORED) as z:
        for rel, content in f.items():
            info = zipfile.ZipInfo(root + "/" + rel, date_time=time.localtime()[:6])
            info.compress_type = zipfile.ZIP_STORED
            info.external_attr = (0o755 if is_exec(rel) else 0o644) << 16
            info.create_system = 3
            z.writestr(info, content.encode("utf-8"))
    return len(f)


def write_dir(s, out_dir):
    f = final_files(s)
    root = Path(out_dir) / (s["meta"]["name"] or "plugin")
    root.mkdir(parents=True, exist_ok=True)
    for rel, content in f.items():
        p = root / rel
        p.parent.mkdir(parents=True, exist_ok=True)
        p.write_text(content, encoding="utf-8")
        if is_exec(rel) and os.name != "nt":
            try:
                p.chmod(0o755)
            except OSError:
                pass
    return root, len(f)


# ---------------------------------------------------------------------------
# 六、工程 / 配置持久化
# ---------------------------------------------------------------------------

def load_config():
    try:
        return json.loads(CONFIG_PATH.read_text(encoding="utf-8"))
    except Exception:
        return {}


def save_config(cfg):
    try:
        CONFIG_PATH.parent.mkdir(parents=True, exist_ok=True)
        CONFIG_PATH.write_text(json.dumps(cfg, ensure_ascii=False, indent=2), encoding="utf-8")
    except Exception:
        pass


def export_project(s, path):
    Path(path).write_text(json.dumps({"version": 1, "app": APP_VER, "state": s},
                                     ensure_ascii=False, indent=2), encoding="utf-8")


def import_project(path):
    o = json.loads(Path(path).read_text(encoding="utf-8"))
    if "state" not in o:
        raise ValueError("不是有效的工程文件（缺少 state）")
    s = blank_state()
    deep_update(s, o["state"])
    s["codeOverride"] = s.get("codeOverride") or {}
    return s


# ---------------------------------------------------------------------------
# 七、GUI 通用组件（tkinter 标准库，Windows / Linux / macOS 通用）
# ---------------------------------------------------------------------------

import tkinter as tk
from tkinter import ttk, font as tkfont, messagebox, filedialog, scrolledtext

C = {
    "bg": "#eef1f7", "panel": "#ffffff", "line": "#d6dceb", "text": "#1c2540",
    "muted": "#6b7699", "accent": "#2f6bff", "accent2": "#22d3ee",
    "dark": "#101735", "dark2": "#182248", "darkline": "#2a3560",
    "ok": "#16834a", "bad": "#cf2f3e", "warn": "#b7791f",
    "chip": "#e8efff", "chip2": "#e6fbf7", "chip3": "#fff3d6",
}
_FONT_CACHE = {}


def ui_font(size=10, bold=False, mono=False):
    key = (size, bold, mono)
    if key in _FONT_CACHE:
        return _FONT_CACHE[key]
    root = None
    try:
        root = tk._default_root
        fams = set(tkfont.families(root)) if root else set()
    except Exception:
        fams = set()
    if mono:
        cand = ["Consolas", "DejaVu Sans Mono", "Courier New", "Menlo", "monospace"]
    else:
        cand = ["Microsoft YaHei", "Noto Sans CJK SC", "WenQuanYi Micro Hei",
                "PingFang SC", "Segoe UI", "DejaVu Sans"]
    fam = next((f for f in cand if f in fams), "TkDefaultFont" if not root else tkfont.nametofont("TkDefaultFont").actual()["family"])
    out = (fam, size, "bold" if bold else "normal")
    _FONT_CACHE[key] = out
    return out


def btn(parent, text, cmd, kind="normal", **kw):
    styles = {
        "normal": dict(bg="#ffffff", fg=C["text"], activebackground="#e8efff"),
        "primary": dict(bg=C["accent"], fg="#ffffff", activebackground="#2556d6"),
        "ghost": dict(bg=C["dark2"], fg="#dbe4ff", activebackground="#26345f"),
        "danger": dict(bg="#fdecee", fg=C["bad"], activebackground="#fbd8dc"),
    }
    st = styles[kind]
    b = tk.Button(parent, text=text, command=cmd, font=ui_font(10), relief="flat",
                  bd=0, padx=12, pady=6, cursor="hand2", highlightthickness=0, **st, **kw)
    return b


class ScrollFrame(tk.Frame):
    """带滚动条的容器；内容放 self.inner。跨平台滚轮绑定。"""

    def __init__(self, parent, bg=None, **kw):
        bg = bg or C["panel"]
        super().__init__(parent, bg=bg, **kw)
        self.canvas = tk.Canvas(self, bg=bg, highlightthickness=0, bd=0)
        self.sb = ttk.Scrollbar(self, orient="vertical", command=self.canvas.yview)
        self.inner = tk.Frame(self.canvas, bg=bg)
        self._win = self.canvas.create_window((0, 0), window=self.inner, anchor="nw")
        self.canvas.configure(yscrollcommand=self.sb.set)
        self.canvas.pack(side="left", fill="both", expand=True)
        self.sb.pack(side="right", fill="y")
        self.inner.bind("<Configure>", self._on_inner)
        self.canvas.bind("<Configure>", self._on_canvas)
        for w in (self.canvas, self.inner, self):
            w.bind("<MouseWheel>", self._wheel)
            w.bind("<Button-4>", self._wheel)
            w.bind("<Button-5>", self._wheel)
        self._bind_tree(self.inner)

    def _bind_tree(self, w):
        for ch in w.winfo_children():
            ch.bind("<MouseWheel>", self._wheel)
            ch.bind("<Button-4>", self._wheel)
            ch.bind("<Button-5>", self._wheel)
            self._bind_tree(ch)

    def rebind(self):
        self._bind_tree(self.inner)

    def _on_inner(self, e=None):
        self.canvas.configure(scrollregion=self.canvas.bbox("all"))

    def _on_canvas(self, e=None):
        self.canvas.itemconfig(self._win, width=e.width)

    def _wheel(self, e):
        if e.num == 4:
            delta = -1
        elif e.num == 5:
            delta = 1
        else:
            delta = -1 if e.delta > 0 else 1
        self.canvas.yview_scroll(delta, "units")
        return "break"

    def clear(self):
        for w in self.inner.winfo_children():
            w.destroy()


TUTORIAL_STEPS = [
    dict(title="欢迎使用插件制作器", tag="01 / 06", color="#2f6bff",
         lines=["这是 NebulaDrive 插件平台 v2 的图形化制作工具。",
                "不用懂 Go、不用装 Go，也不用联网：拖控件 → 校验 → 一键构建 ZIP。",
                "最终产物解压到 plugins/<插件名>/ 就能被网盘装载。",
                "本工具用 Python + tkinter 写成，Windows / Linux / macOS 都能跑。"],
         tip="第一次打开先看这 6 步，约 1 分钟。以后可从「帮助 → 新手教程」重看。"),
    dict(title="先记住插件的三文件契约", tag="02 / 06", color="#7c3aed",
         lines=["① plugin.json  —— 我是谁、我要什么（身份 / 钩子 / 页面 / 能力）",
                "② register.go  —— 把同样的话用 Go 字面量再说一遍（只读不编译）",
                "③ co.BEGG      —— 怎么跑起来、长什么样（JSON）",
                "三份声明宿主会互相核对，不一致直接拒绝装载；制作器会自动保持同步。",
                "目录形式不限定：ASS/ 里塞一堆 .php + 注册型 .go 也合法。"],
         tip="路径基准永远是 co.BEGG 所在目录：home 写 in.php，不要写 ASS/in.php。"),
    dict(title="设计页：拖控件搭出插件", tag="03 / 06", color="#0ea5e9",
         lines=["左上「控件库」里的项，单击或拖到中间画布即可添加。",
                "「结构控件」决定插件能做什么：页面、设置字段、主题、注册型能力、钩子订阅。",
                "「页面控件」决定插件主页长什么样：标题、卡片、统计、表格、按钮…",
                "点画布里的卡片，在右侧「属性」里改字段；用 ↑ ↓ 调整顺序，✕ 删除。",
                "顶部的三个固定项：插件身份 / 运行时 / 目录布局，也要点开填。"],
         tip="「钩子订阅」控件会打开 108 个钩子的选择器，可搜索、可批量勾选。"),
    dict(title="代码页：也可以直接写代码", tag="04 / 06", color="#16a34a",
         lines=["每个生成的文件都能在「代码」页直接编辑，改完自动生效。",
                "「重置为自动生成」丢弃手工改动；「同步回设计」把 plugin.json / co.BEGG",
                "  的手工修改反向解析回控件（其它文件只保留手工内容）。",
                "想写 PHP 逻辑？选「自定义 HTML」控件，或直接编辑 in.php。"],
         tip="手工编辑过的文件在文件名上会标 ●，构建时会覆盖自动生成内容。"),
    dict(title="校验与一键构建", tag="05 / 06", color="#f59e0b",
         lines=["「校验」页模拟宿主的 ParseManifest / ParseBegg / crossCheck / ValidateFiles。",
                "出现红字时构建会被阻止，点条目看原因，回到设计页修正即可。",
                "「构建」页能预览完整目录树和每个文件的内容。",
                "点「一键构建 ZIP」下载插件包；也可「导出到目录」直接联调。"],
         tip="订阅了钩子的插件会生成 plugin.py 骨架；Linux 部署记得 chmod +x。"),
    dict(title="安装与安全提醒", tag="06 / 06", color="#dc2626",
         lines=["1. 解压 ZIP 到 plugins/<插件名>/。",
                "2. 后台 → 插件 → 开启全局总开关（需勾选风险确认）。",
                "3. 启用该插件。插件默认关闭，这是刻意设计。",
                "宿主对 /ext/* 完全透传鉴权：不校验登录态、不注入 token、不设 sandbox。",
                "插件页面必须自己鉴权；开启插件 = 把服务器执行权限交给插件作者。",
                "缺 PHP / Python / Node 等解释器时，宿主会在启用时明确报错，不静默降级。"],
         tip="只装你信任的插件。现在可以点「开始使用」，左上模板库有 20 套模板可直接套用。"),
]


class Tutorial(tk.Toplevel):
    """首次启动的新手教程：分步引导，可跳过，可设置不再显示。"""

    def __init__(self, master, on_done=None, auto_close_ms=0):
        super().__init__(master)
        self.title("新手教程 · NebulaDrive 插件制作器")
        self.configure(bg=C["bg"])
        self.resizable(False, False)
        self.on_done = on_done
        self.step = 0
        self.transient(master)
        self.grab_set()
        self.protocol("WM_DELETE_WINDOW", self.finish)

        head = tk.Frame(self, bg=C["dark"], height=86)
        head.pack(fill="x")
        head.pack_propagate(False)
        self.tag = tk.Label(head, text="", font=ui_font(10, bold=True), bg=C["dark"], fg="#8ea4ff")
        self.tag.pack(anchor="w", padx=22, pady=(14, 0))
        self.head_title = tk.Label(head, text="", font=ui_font(16, bold=True), bg=C["dark"], fg="#ffffff")
        self.head_title.pack(anchor="w", padx=22, pady=(2, 12))

        body = tk.Frame(self, bg=C["panel"], padx=24, pady=18)
        body.pack(fill="both", expand=True)
        self.lines_box = tk.Frame(body, bg=C["panel"])
        self.lines_box.pack(fill="both", expand=True)
        self.tipbox = tk.Label(body, text="", font=ui_font(10), bg="#fff8e6", fg="#8a5b00",
                               justify="left", wraplength=640, padx=12, pady=10, anchor="w")
        self.tipbox.pack(fill="x", pady=(14, 0))

        foot = tk.Frame(self, bg=C["bg"], padx=18, pady=12)
        foot.pack(fill="x")
        self.dots = tk.Label(foot, text="", font=ui_font(10), bg=C["bg"], fg=C["muted"])
        self.dots.pack(side="left")
        self.var_noshow = tk.BooleanVar(value=True)
        tk.Checkbutton(foot, text="下次不再自动显示（帮助里可重开）", variable=self.var_noshow,
                       font=ui_font(9), bg=C["bg"], fg=C["muted"],
                       activebackground=C["bg"], selectcolor="#ffffff").pack(side="left", padx=18)
        btn(foot, "跳过", self.finish).pack(side="right", padx=(6, 0))
        self.nextb = btn(foot, "下一步 →", self.next_step, kind="primary")
        self.nextb.pack(side="right", padx=6)
        self.prevb = btn(foot, "← 上一步", self.prev_step)
        self.prevb.pack(side="right", padx=6)

        self.update_idletasks()
        w, h = 720, 470
        x = master.winfo_rootx() + max(0, (master.winfo_width() - w) // 2)
        y = master.winfo_rooty() + max(0, (master.winfo_height() - h) // 2)
        self.geometry("%dx%d+%d+%d" % (w, h, x, y))
        self.render()
        if auto_close_ms:
            self.after(auto_close_ms, self.finish)

    def render(self):
        s = TUTORIAL_STEPS[self.step]
        self.tag.configure(text=s["tag"] + "   ·   " + s["title"])
        self.head_title.configure(text=s["title"])
        for w in self.lines_box.winfo_children():
            w.destroy()
        for i, line in enumerate(s["lines"]):
            row = tk.Frame(self.lines_box, bg=C["panel"])
            row.pack(fill="x", pady=5)
            tk.Label(row, text="•", font=ui_font(12, bold=True), bg=C["panel"],
                     fg=s["color"], width=2, anchor="n").pack(side="left")
            tk.Label(row, text=line, font=ui_font(11), bg=C["panel"], fg=C["text"],
                     justify="left", wraplength=620, anchor="w").pack(side="left", fill="x", expand=True)
        self.tipbox.configure(text="💡 " + s["tip"])
        self.dots.configure(text="第 %d / %d 步" % (self.step + 1, len(TUTORIAL_STEPS)))
        self.prevb.configure(state="disabled" if self.step == 0 else "normal")
        self.nextb.configure(text="开始使用 ✓" if self.step == len(TUTORIAL_STEPS) - 1 else "下一步 →")

    def next_step(self):
        if self.step < len(TUTORIAL_STEPS) - 1:
            self.step += 1
            self.render()
        else:
            self.finish()

    def prev_step(self):
        if self.step > 0:
            self.step -= 1
            self.render()

    def finish(self):
        if self.on_done:
            try:
                # 勾选「下次不再自动显示」= 记住已读，之后不再自动弹教程。
                self.on_done(bool(self.var_noshow.get()))
            except Exception:
                pass
        try:
            self.grab_release()
        except Exception:
            pass
        self.destroy()


# ---------------------------------------------------------------------------
# 八、主窗口
# ---------------------------------------------------------------------------

class App(tk.Tk):
    def __init__(self, smoke=False):
        super().__init__()
        self.smoke = smoke
        self.title("%s  v%s  ·  Python 桌面版（Windows / Linux / macOS）" % (APP_NAME, APP_VER))
        self.configure(bg=C["bg"])
        self.geometry("1240x800")
        self.minsize(1020, 660)
        if os.name == "nt":
            try:
                import ctypes
                ctypes.windll.shcore.SetProcessDpiAwareness(1)
            except Exception:
                pass

        cfg = load_config()
        state = cfg.get("state")
        if state:
            try:
                self.S = blank_state()
                deep_update(self.S, state)
                self.S["codeOverride"] = self.S.get("codeOverride") or {}
            except Exception:
                self.S = apply_template("hello-php")
        else:
            self.S = apply_template("hello-php")
        self.template_key = cfg.get("last_template", "hello-php")
        self.sel = None
        self.cv = "struct"
        self.code_file = "plugin.json"
        self.build_sel = None
        self._save_job = None
        self._ghost = None
        self._drag = None

        self._build_menu()
        self._build_toolbar()
        self._build_notebook()
        self._build_status()
        self.refresh_all()
        self.protocol("WM_DELETE_WINDOW", self.on_close)

        if not cfg.get("tutorial_seen"):
            if smoke:
                print("[smoke] 首次启动教程：已弹出")
            self.after(220, lambda: Tutorial(self, self.on_tutorial_done,
                                              auto_close_ms=1400 if smoke else 0))
        elif smoke:
            print("[smoke] 首次启动教程：已跳过（tutorial_seen=true）")
        if smoke:
            self.after(2200, self.on_close)

    # ---- 菜单 ----
    def _build_menu(self):
        mb = tk.Menu(self)
        fm = tk.Menu(mb, tearoff=0, font=ui_font(10))
        fm.add_command(label="新建空白工程", command=lambda: self.load_template("blank"))
        fm.add_command(label="导入工程 .json …", command=self.on_import)
        fm.add_command(label="导出工程 .json …", command=self.on_export_project)
        fm.add_separator()
        fm.add_command(label="导出插件 ZIP …", command=self.on_build_zip)
        fm.add_command(label="导出到目录 …", command=self.on_build_dir)
        fm.add_separator()
        fm.add_command(label="退出", command=self.on_close)
        mb.add_cascade(label="文件", menu=fm)

        tm = tk.Menu(mb, tearoff=0, font=ui_font(10))
        for g in TEMPLATE_GROUPS:
            sub = tk.Menu(tm, tearoff=0, font=ui_font(10))
            for t in g["items"]:
                sub.add_command(label=t["name"], command=lambda k=t["key"]: self.load_template(k))
            tm.add_cascade(label=g["name"] + "（%d）" % len(g["items"]), menu=sub)
        mb.add_cascade(label="模板库（%d 套）" % len(TEMPLATES), menu=tm)

        hm = tk.Menu(mb, tearoff=0, font=ui_font(10))
        hm.add_command(label="新手教程（首次启动引导）", command=lambda: Tutorial(self, self.on_tutorial_done))
        hm.add_command(label="99 个钩子选择器", command=self.open_hook_picker)
        hm.add_separator()
        hm.add_command(label="自检：全部模板生成 + 校验", command=self.on_selftest_gui)
        hm.add_command(label="关于", command=self.on_about)
        mb.add_cascade(label="帮助", menu=hm)
        self.config(menu=mb)

    # ---- 工具栏 ----
    def _build_toolbar(self):
        bar = tk.Frame(self, bg=C["dark"], height=54)
        bar.pack(fill="x")
        bar.pack_propagate(False)
        tk.Label(bar, text="◈", font=ui_font(18, bold=True), bg=C["dark"], fg=C["accent2"]).pack(side="left", padx=(16, 4))
        tk.Label(bar, text="插件制作器", font=ui_font(12, bold=True), bg=C["dark"], fg="#ffffff").pack(side="left", padx=(0, 18))

        tk.Label(bar, text="模板", font=ui_font(9), bg=C["dark"], fg="#9fb0e0").pack(side="left")
        self.tpl_var = tk.StringVar()
        self.tpl_combo = ttk.Combobox(bar, textvariable=self.tpl_var, state="readonly",
                                      width=34, font=ui_font(10))
        self.tpl_combo["values"] = ["%s · %s" % (t["group"], t["name"]) for t in TEMPLATES]
        self.tpl_combo.pack(side="left", padx=(6, 6))
        self.tpl_combo.bind("<<ComboboxSelected>>", self.on_template_selected)
        btn(bar, "教程", lambda: Tutorial(self, self.on_tutorial_done), kind="ghost").pack(side="left", padx=3)
        btn(bar, "导入", self.on_import, kind="ghost").pack(side="left", padx=3)
        btn(bar, "导出工程", self.on_export_project, kind="ghost").pack(side="left", padx=3)

        right = tk.Frame(bar, bg=C["dark"])
        right.pack(side="right", padx=12)
        btn(right, "校验", self.on_goto_check, kind="ghost").pack(side="left", padx=3)
        btn(right, "一键构建 ZIP", self.on_build_zip, kind="primary").pack(side="left", padx=3)

    def _build_notebook(self):
        self.nb = ttk.Notebook(self)
        self.nb.pack(fill="both", expand=True, padx=8, pady=(8, 2))
        self.tab_design = tk.Frame(self.nb, bg=C["bg"])
        self.tab_code = tk.Frame(self.nb, bg=C["bg"])
        self.tab_check = tk.Frame(self.nb, bg=C["bg"])
        self.tab_build = tk.Frame(self.nb, bg=C["bg"])
        self.tab_preview = tk.Frame(self.nb, bg=C["bg"])
        self.nb.add(self.tab_design, text="  ① 设计（拖拽控件）  ")
        self.nb.add(self.tab_code, text="  ② 代码  ")
        self.nb.add(self.tab_check, text="  ③ 校验  ")
        self.nb.add(self.tab_build, text="  ④ 构建  ")
        self.nb.add(self.tab_preview, text="  ⑤ 页面预览  ")
        self._build_design_tab()
        self._build_code_tab()
        self._build_check_tab()
        self._build_build_tab()
        self._build_preview_tab()

    def _build_status(self):
        st = tk.Frame(self, bg="#dfe5f2", height=28)
        st.pack(fill="x", side="bottom")
        st.pack_propagate(False)
        self.status = tk.Label(st, text="", font=ui_font(9), bg="#dfe5f2", fg=C["muted"], anchor="w")
        self.status.pack(side="left", padx=12)
        self.status_right = tk.Label(st, text="纯标准库 · 离线 · 跨平台", font=ui_font(9),
                                     bg="#dfe5f2", fg=C["muted"])
        self.status_right.pack(side="right", padx=12)

    def set_status(self, text):
        self.status.configure(text=text)

    # ---- 教程回调 / 关闭 ----
    def on_tutorial_done(self, no_more):
        cfg = load_config()
        if no_more:
            cfg["tutorial_seen"] = True
            save_config(cfg)
        self.set_status("新手教程已看完 · 从左上模板库挑一套模板开始吧")

    def on_close(self):
        if getattr(self, "_closed", False):
            return
        self._closed = True
        try:
            cfg = load_config()
            cfg["state"] = self.S
            cfg["last_template"] = self.template_key
            save_config(cfg)
        except Exception:
            pass
        try:
            self.destroy()
        except Exception:
            pass

    def _schedule_save(self):
        if self._save_job:
            try:
                self.after_cancel(self._save_job)
            except Exception:
                pass
        self._save_job = self.after(700, self._do_save)

    def _do_save(self):
        self._save_job = None
        cfg = load_config()
        cfg["state"] = self.S
        cfg["last_template"] = self.template_key
        save_config(cfg)

    def changed(self, refresh=("canvas", "preview", "code", "check", "build")):
        self._schedule_save()
        self.refresh(refresh)

    def refresh(self, which):
        if "canvas" in which:
            self.refresh_canvas()
        if "inspect" in which:
            self.refresh_inspect()
        if "preview" in which:
            self.refresh_preview()
        if "code" in which:
            self.refresh_code()
        if "check" in which:
            self.refresh_check()
        if "build" in which:
            self.refresh_build()

    def refresh_all(self):
        idx = next((i for i, t in enumerate(TEMPLATES) if t["key"] == self.template_key), 0)
        self.tpl_combo.current(idx)
        self.tpl_var.set(self.tpl_combo["values"][idx])
        self.refresh_palette()
        self.refresh_canvas()
        self.refresh_inspect()
        self.refresh_preview()
        self.refresh_code()
        self.refresh_check()
        self.refresh_build()
        self.refresh_status()

    def refresh_status(self):
        r = validate(self.S)
        bad = [x for x in r if not x["ok"] and not x["warn"]]
        t = TEMPLATE_BY_KEY.get(self.template_key)
        self.set_status("模板：%s  ·  控件 %d 个  ·  校验 %d 项，%s" % (
            t["name"] if t else "自定义", len(self.item_list()), len(r),
            "全部通过 ✓" if not bad else "%d 项未通过 ✕" % len(bad)))

    # ---- 模板 ----
    def on_template_selected(self, e=None):
        i = self.tpl_combo.current()
        if 0 <= i < len(TEMPLATES):
            self.load_template(TEMPLATES[i]["key"])

    def load_template(self, key):
        if key not in TEMPLATE_BY_KEY:
            return
        self.S = apply_template(key)
        self.template_key = key
        self.sel = None
        self.code_file = "plugin.json"
        self.build_sel = None
        self.cv = "struct"
        self.refresh_all()
        self.set_status("已加载模板：%s —— %s" % (TEMPLATE_BY_KEY[key]["name"], TEMPLATE_BY_KEY[key]["desc"]))

    def on_import(self):
        p = filedialog.askopenfilename(title="导入工程文件", filetypes=[("工程文件", "*.json"), ("全部", "*.*")])
        if not p:
            return
        try:
            self.S = import_project(p)
            self.template_key = ""
            self.sel = None
            self.code_file = "plugin.json"
            self.refresh_all()
            self.set_status("工程已导入：" + p)
        except Exception as e:
            messagebox.showerror("导入失败", str(e))

    def on_export_project(self):
        p = filedialog.asksaveasfilename(title="导出工程文件", defaultextension=".json",
                                         initialfile=(self.S["meta"]["name"] or "plugin") + ".pluginproj.json",
                                         filetypes=[("工程文件", "*.json")])
        if not p:
            return
        export_project(self.S, p)
        self.set_status("工程已导出：" + p)

    # ---- 页签跳转 ----
    def on_goto_check(self):
        self.nb.select(self.tab_check)
        self.refresh_check()

    def on_about(self):
        messagebox.showinfo("关于", "%s v%s\n\n" % (APP_NAME, APP_VER) +
                            "纯 Python 标准库（tkinter）写的跨平台桌面窗口。\n"
                            "内置 %d 套模板、%d 个钩子、拖拽控件与代码双模式。\n\n" % (len(TEMPLATES), len(HOOKS)) +
                            "生成物对齐 NebulaDrive 插件协议 v2 三文件契约。")

    def on_selftest_gui(self):
        ok, msg = run_selftest(verbose=False)
        (messagebox.showinfo if ok else messagebox.showerror)("模板自检", msg)

    # ---- 一键构建 ----
    def on_build_zip(self):
        bad = [x for x in validate(self.S) if not x["ok"] and not x["warn"]]
        if bad:
            self.nb.select(self.tab_check)
            self.refresh_check()
            messagebox.showwarning("构建被阻止", "有 %d 项校验未通过，请先修正：\n\n%s" %
                                   (len(bad), "\n".join("· " + x["name"] for x in bad[:8])))
            return
        p = filedialog.asksaveasfilename(title="导出插件 ZIP", defaultextension=".zip",
                                         initialfile=(self.S["meta"]["name"] or "plugin") + ".zip",
                                         filetypes=[("插件包", "*.zip")])
        if not p:
            return
        try:
            n = make_zip(self.S, p)
            self.set_status("已导出 %s（%d 个文件）" % (p, n))
            messagebox.showinfo("构建成功", "插件包已生成：\n%s\n\n共 %d 个文件。\n\n"
                                "解压到 plugins/%s/ 后，去后台开启全局总开关并启用插件。" %
                                (p, n, self.S["meta"]["name"]))
        except Exception as e:
            messagebox.showerror("构建失败", str(e))

    def on_build_dir(self):
        bad = [x for x in validate(self.S) if not x["ok"] and not x["warn"]]
        if bad:
            self.nb.select(self.tab_check)
            self.refresh_check()
            messagebox.showwarning("构建被阻止", "有 %d 项校验未通过，请先修正。" % len(bad))
            return
        d = filedialog.askdirectory(title="选择导出到的父目录")
        if not d:
            return
        try:
            root, n = write_dir(self.S, d)
            self.set_status("已导出目录 %s（%d 个文件）" % (root, n))
            messagebox.showinfo("导出成功", "插件目录：\n%s\n\n共 %d 个文件。" % (root, n))
        except Exception as e:
            messagebox.showerror("导出失败", str(e))


# ---------------------------------------------------------------------------
# 九、页签实现（以 mixin 形式挂到 App 上，保持单文件结构清晰）
# ---------------------------------------------------------------------------

def app__build_design_tab(self):
    top = tk.Frame(self.tab_design, bg=C["bg"])
    top.pack(fill="x", padx=8, pady=8)
    self.cv_var = tk.StringVar(value="struct")
    for val, text in (("struct", "结构画布 · 插件能做什么"), ("page", "页面画布 · 主页长什么样")):
        tk.Radiobutton(top, text=text, variable=self.cv_var, value=val,
                       font=ui_font(10, bold=True), bg=C["bg"], fg=C["text"],
                       selectcolor="#ffffff", activebackground=C["bg"],
                       command=self.on_canvas_mode).pack(side="left", padx=(0, 14))
    for key, text in (("meta", "◆ 插件身份"), ("runtime", "◇ 运行时"), ("layout", "▧ 目录布局")):
        btn(top, text, lambda k=key: self.select_item(k), kind="normal").pack(side="left", padx=3)
    tk.Label(top, text="　单击或拖动左侧控件到中间画布 →", font=ui_font(9),
             bg=C["bg"], fg=C["muted"]).pack(side="left", padx=8)

    body = tk.Frame(self.tab_design, bg=C["bg"])
    body.pack(fill="both", expand=True, padx=8, pady=(0, 8))
    left = tk.Frame(body, bg=C["panel"], width=218, highlightbackground=C["line"], highlightthickness=1)
    left.pack(side="left", fill="y")
    left.pack_propagate(False)
    mid = tk.Frame(body, bg=C["bg"])
    mid.pack(side="left", fill="both", expand=True, padx=8)
    right = tk.Frame(body, bg=C["panel"], width=310, highlightbackground=C["line"], highlightthickness=1)
    right.pack(side="right", fill="y")
    right.pack_propagate(False)

    tk.Label(left, text="控件库", font=ui_font(11, bold=True), bg=C["panel"],
             fg=C["text"], anchor="w").pack(fill="x", padx=10, pady=(10, 2))
    self.palette = ScrollFrame(left)
    self.palette.pack(fill="both", expand=True)

    self.canvas_hint = tk.Label(mid, text="", font=ui_font(9), bg=C["bg"], fg=C["muted"], anchor="w")
    self.canvas_hint.pack(fill="x", pady=(0, 4))
    self.canvas_box = ScrollFrame(mid, bg=C["bg"])
    self.canvas_box.pack(fill="both", expand=True)

    tk.Label(right, text="属性", font=ui_font(11, bold=True), bg=C["panel"],
             fg=C["text"], anchor="w").pack(fill="x", padx=10, pady=(10, 2))
    self.sel_name = tk.Label(right, text="未选中", font=ui_font(9), bg=C["panel"],
                             fg=C["muted"], anchor="w")
    self.sel_name.pack(fill="x", padx=10, pady=(0, 6))
    self.inspect = ScrollFrame(right)
    self.inspect.pack(fill="both", expand=True)


def app_on_canvas_mode(self):
    self.cv = self.cv_var.get()
    self.sel = None
    self.refresh_palette()
    self.refresh_canvas()
    self.refresh_inspect()
    self.set_status("已切换到：" + ("结构画布" if self.cv == "struct" else "页面画布"))


def app_refresh_palette(self):
    self.cv_var.set(self.cv)
    sf = self.palette
    sf.clear()
    parent = sf.inner

    def group(title):
        tk.Label(parent, text=title, font=ui_font(9, bold=True), bg=C["panel"],
                 fg=C["muted"], anchor="w").pack(fill="x", padx=8, pady=(10, 3))

    def item(ctl, name, sub, desc, icon="＋"):
        f = tk.Frame(parent, bg="#f7f9ff", highlightbackground=C["line"], highlightthickness=1,
                     cursor="hand2")
        f.pack(fill="x", padx=8, pady=3)
        tk.Label(f, text=icon, font=ui_font(12, bold=True), bg="#f7f9ff", fg=C["accent"],
                 width=3, anchor="w").pack(side="left", padx=(6, 2), pady=6)
        box = tk.Frame(f, bg="#f7f9ff")
        box.pack(side="left", fill="x", expand=True, pady=4)
        tk.Label(box, text=name, font=ui_font(10, bold=True), bg="#f7f9ff",
                 fg=C["text"], anchor="w").pack(fill="x")
        tk.Label(box, text=sub, font=ui_font(8), bg="#f7f9ff", fg=C["muted"],
                 anchor="w").pack(fill="x")
        tk.Label(f, text="拖拽", font=ui_font(8), bg="#f7f9ff", fg=C["accent"]).pack(side="right", padx=6)
        for w in (f, box) + tuple(box.winfo_children()) + tuple(f.winfo_children()):
            if isinstance(w, tk.Widget):
                w.bind("<ButtonPress-1>", lambda e, t=ctl: self._drag_start(e, t))
                w.bind("<B1-Motion>", self._drag_motion)
                w.bind("<ButtonRelease-1>", self._drag_release)
        return f

    if self.cv == "struct":
        group("固定项（点击编辑）")
        item("meta", "插件身份", "name / 版本 / 作者", "", "◆")
        item("runtime", "运行时 / 解释器", "co.BEGG", "", "◇")
        item("layout", "目录布局", "扁平 / ASS/", "", "▧")
        group("页面与 UI 控件")
        for t, name, sub, desc in STRUCT_CONTROLS[:4]:
            item(t, name, sub, desc)
        group("能力与钩子控件")
        for t, name, sub, desc in STRUCT_CONTROLS[4:6]:
            item(t, name, sub, desc)
        group("文件控件")
        for t, name, sub, desc in STRUCT_CONTROLS[6:]:
            item(t, name, sub, desc)
    else:
        group("页面控件（决定主页长什么样）")
        for t, name, desc in WIDGET_CONTROLS:
            item(t, name, desc, "", "▦")
        group("提示")
        tk.Label(parent, text="页面控件会渲染成插件主页的 HTML / PHP。\n"
                              "可在「代码」页继续改写，或用「自定义 HTML」\n直接写 PHP 片段。",
                 font=ui_font(9), bg=C["panel"], fg=C["muted"], justify="left",
                 wraplength=190).pack(fill="x", padx=8, pady=6)
    sf.rebind()
    sf._on_inner()
    self.canvas_hint.configure(text=(
        "结构画布：拖入「页面 / 设置字段 / 钩子订阅 / 注册型能力」等控件，点卡片在右侧改属性"
        if self.cv == "struct" else
        "页面画布：拖入「标题 / 卡片 / 统计 / 表格」等控件，下方「页面预览」实时更新"))


def app__drag_start(self, e, ctl):
    self._drag = {"ctl": ctl, "x0": e.x_root, "y0": e.y_root, "moved": False}


def app__show_ghost(self, x, y, ctl):
    label = next((n for t, n, *_ in STRUCT_CONTROLS + WIDGET_CONTROLS if t == ctl), ctl)
    if self._ghost is None:
        g = tk.Toplevel(self)
        g.overrideredirect(True)
        try:
            g.attributes("-topmost", True)
            g.attributes("-alpha", 0.92)
        except Exception:
            pass
        tk.Label(g, text="", font=ui_font(10, bold=True), bg=C["accent"], fg="#ffffff",
                 padx=12, pady=7).pack()
        self._ghost = g
    self._ghost.winfo_children()[0].configure(text="＋ " + label)
    self._ghost.geometry("+%d+%d" % (x + 14, y + 12))


def app__hide_ghost(self):
    if self._ghost is not None:
        try:
            self._ghost.destroy()
        except Exception:
            pass
        self._ghost = None


def app__drag_motion(self, e):
    d = self._drag
    if not d:
        return
    if abs(e.x_root - d["x0"]) + abs(e.y_root - d["y0"]) > 8:
        d["moved"] = True
        self._show_ghost(e.x_root, e.y_root, d["ctl"])


def app__drag_release(self, e):
    d, self._drag = self._drag, None
    self._hide_ghost()
    if not d:
        return
    if d["moved"]:
        c = self.canvas_box
        x, y = e.x_root, e.y_root
        inside = (c.winfo_rootx() <= x <= c.winfo_rootx() + c.winfo_width() and
                  c.winfo_rooty() <= y <= c.winfo_rooty() + c.winfo_height())
        if not inside:
            self.set_status("把控件拖到中间画布再松手即可添加（直接单击控件也可以）")
            return
    self.add_control(d["ctl"])


def app_item_list(self):
    S = self.S
    out = []
    if self.cv == "page":
        for i, w in enumerate(S["widgets"]):
            out.append({"key": "w%d" % i, "type": w["t"], "obj": w, "idx": i})
        return out
    for i, p in enumerate(S["pages"]):
        out.append({"key": "p%d" % i, "type": "page", "obj": p, "idx": i})
    for i, p in enumerate(S["sidebar"]):
        out.append({"key": "s%d" % i, "type": "sidebar", "obj": p, "idx": i})
    for i, p in enumerate(S["settings"]):
        out.append({"key": "t%d" % i, "type": "setting", "obj": p, "idx": i})
    if S["theme"]:
        out.append({"key": "theme", "type": "theme", "obj": S["theme"], "idx": -1})
    for i, p in enumerate(S["providers"]):
        out.append({"key": "r%d" % i, "type": "provider", "obj": p, "idx": i})
    if S["hooks"]:
        out.append({"key": "hooks", "type": "hookgroup", "obj": {"n": len(S["hooks"])}, "idx": -1})
    for i, p in enumerate(S["customFiles"]):
        out.append({"key": "f%d" % i, "type": "customfile", "obj": p, "idx": i})
    return out


def app_array_for(self, typ):
    if typ.startswith("w-"):
        return self.S["widgets"]
    return {"page": self.S["pages"], "sidebar": self.S["sidebar"],
            "setting": self.S["settings"], "provider": self.S["providers"],
            "customfile": self.S["customFiles"]}.get(typ)


def app_find_item(self, key):
    return next((x for x in self.item_list() if x["key"] == key), None)


def app_card_info(self, it):
    o, t = it["obj"], it["type"]
    if t == "page":
        return {"title": o.get("title") or o.get("name"), "chip": "Page", "col": C["chip"],
                "desc": "%s · %s · %s" % (o.get("area"), o.get("home") or o.get("url") or "/", o.get("embed") or "iframe")}
    if t == "sidebar":
        return {"title": o.get("title") or o.get("page"), "chip": "UI", "col": C["chip2"],
                "desc": "覆盖 %s · %s · order %s" % (o.get("page"), o.get("area") or "main", o.get("order", 0))}
    if t == "setting":
        return {"title": o.get("label") or o.get("key"), "chip": "UI", "col": C["chip2"],
                "desc": "%s · key=%s%s" % (o.get("type"), o.get("key"), " · 默认 " + str(o["default"]) if o.get("default") else "")}
    if t == "theme":
        return {"title": o.get("label") or o.get("name"), "chip": "主题", "col": C["chip3"],
                "desc": "%d 个 CSS 变量%s" % (len(o.get("variables") or {}), " · " + o["css"] if o.get("css") else "")}
    if t == "provider":
        return {"title": "%s:%s" % (o.get("kind"), o.get("name")), "chip": "Provider", "col": C["chip2"],
                "desc": o.get("label") or PROVIDER_DEFAULT_HOOK.get(o.get("kind"), "")}
    if t == "hookgroup":
        return {"title": "已订阅 %d 个钩子" % o["n"], "chip": "Hook", "col": C["chip3"],
                "desc": ", ".join(self.S["hooks"][:5]) + (" …" if o["n"] > 5 else "")}
    if t == "customfile":
        return {"title": o.get("path") or "(未命名)", "chip": "文件", "col": C["chip"],
                "desc": (o.get("content") or "").split("\n")[0][:58]}
    return {"title": TYPE_LABEL.get(t, t), "chip": "控件", "col": C["chip"],
            "desc": (o.get("text") or o.get("sub") or "")[:60]}


def app_refresh_canvas(self):
    sf = self.canvas_box
    sf.clear()
    parent = sf.inner
    items = self.item_list()
    if not items:
        tk.Label(parent, text="◈\n\n画布还是空的\n\n从左侧控件库单击或拖入控件开始设计",
                 font=ui_font(12), bg=C["bg"], fg=C["muted"], justify="center").pack(pady=48)
    for it in items:
        info = self.card_info(it)
        sel = self.sel == it["key"]
        card = tk.Frame(parent, bg="#ffffff", highlightbackground=C["accent"] if sel else C["line"],
                        highlightthickness=2 if sel else 1, cursor="hand2")
        card.pack(fill="x", padx=6, pady=4)
        card._item_key = it["key"]
        grip = tk.Label(card, text="⠿", font=ui_font(13), bg="#ffffff", fg=C["muted"], width=2)
        grip.pack(side="left", padx=(6, 0))
        body = tk.Frame(card, bg="#ffffff")
        body.pack(side="left", fill="x", expand=True, pady=7)
        line = tk.Frame(body, bg="#ffffff")
        line.pack(fill="x")
        tk.Label(line, text=info["chip"], font=ui_font(8, bold=True), bg=info["col"],
                 fg=C["accent"], padx=6, pady=1).pack(side="left", padx=(0, 6))
        tk.Label(line, text=info["title"], font=ui_font(10, bold=True), bg="#ffffff",
                 fg=C["text"], anchor="w").pack(side="left")
        tk.Label(body, text=info["desc"], font=ui_font(9), bg="#ffffff", fg=C["muted"],
                 anchor="w", wraplength=380, justify="left").pack(fill="x")
        ops = tk.Frame(card, bg="#ffffff")
        ops.pack(side="right", padx=4)
        for txt, cmd, col in (("↑", lambda k=it["key"]: self.move_item(k, -1), C["muted"]),
                              ("↓", lambda k=it["key"]: self.move_item(k, 1), C["muted"]),
                              ("✕", lambda k=it["key"]: self.del_item(k), C["bad"])):
            tk.Button(ops, text=txt, command=cmd, font=ui_font(9), relief="flat", bd=0,
                      bg="#ffffff", fg=col, width=2, cursor="hand2",
                      activebackground="#eef2ff").pack(side="left")
        targets = [card, grip, body, line] + list(line.winfo_children()) + list(body.winfo_children())
        for w in targets:
            if isinstance(w, tk.Button):
                continue
            w.bind("<ButtonPress-1>", lambda e, k=it["key"]: self._card_press(e, k))
            w.bind("<B1-Motion>", self._card_motion)
            w.bind("<ButtonRelease-1>", lambda e, k=it["key"]: self._card_release(e, k))
    sf.rebind()
    sf._on_inner()
    self.refresh_status()


def app__card_press(self, e, key):
    self._card_drag = {"key": key, "x0": e.x_root, "y0": e.y_root, "moved": False}


def app__card_motion(self, e):
    d = getattr(self, "_card_drag", None)
    if d and abs(e.x_root - d["x0"]) + abs(e.y_root - d["y0"]) > 12:
        d["moved"] = True


def app__card_release(self, e, key):
    d = getattr(self, "_card_drag", None)
    self._card_drag = None
    if not d:
        return
    if not d["moved"]:
        self.select_item(d["key"])
        return
    w = self.winfo_containing(e.x_root, e.y_root)
    target = None
    while w is not None:
        k = getattr(w, "_item_key", None)
        if k:
            target = k
            break
        p = w.winfo_parent()
        if not p:
            break
        try:
            w = self.nametowidget(p)
        except Exception:
            break
    if target and target != d["key"]:
        self.move_to(d["key"], target)


def app_select_item(self, key):
    self.sel = key
    self.refresh_canvas()
    self.refresh_inspect()


def app_move_item(self, key, direction):
    items = self.item_list()
    i = next((n for n, x in enumerate(items) if x["key"] == key), -1)
    if i < 0:
        return
    j = i + direction
    if j < 0 or j >= len(items):
        return
    a, b = items[i], items[j]
    arr1, arr2 = self.array_for(a["type"]), self.array_for(b["type"])
    if arr1 is None or arr1 is not arr2 or a["idx"] < 0 or b["idx"] < 0:
        return
    arr1[a["idx"]], arr1[b["idx"]] = arr1[b["idx"]], arr1[a["idx"]]
    self.changed(("canvas", "preview", "code", "check", "build"))


def app_move_to(self, src, dst):
    items = self.item_list()
    a = next((x for x in items if x["key"] == src), None)
    b = next((x for x in items if x["key"] == dst), None)
    if not a or not b or a["type"] != b["type"] or a["idx"] < 0 or b["idx"] < 0:
        return
    arr = self.array_for(a["type"])
    if arr is None:
        return
    obj, dst_obj = arr[a["idx"]], arr[b["idx"]]
    arr.pop(a["idx"])
    pos = next((n for n, x in enumerate(arr) if x is dst_obj), len(arr))
    arr.insert(pos, obj)
    self.changed(("canvas", "preview", "code", "check", "build"))


def app_del_item(self, key):
    it = self.find_item(key)
    if not it:
        return
    if it["type"] == "theme":
        self.S["theme"] = None
    elif it["type"] == "hookgroup":
        self.S["hooks"] = []
        if not self.S["pages"] and not self.S["providers"]:
            pass
    else:
        arr = self.array_for(it["type"])
        if arr is not None and 0 <= it["idx"] < len(arr):
            arr.pop(it["idx"])
    self.sel = None
    self.changed(("canvas", "preview", "code", "check", "build", "inspect"))


def app_add_control(self, t):
    import random
    uid = lambda: "%04x" % random.randrange(0, 65536)
    S = self.S
    if t == "hook":
        self.open_hook_picker()
        return
    if t == "page":
        o = _p("page" + uid(), "新页面", "page" + uid() + ".php")
        S["pages"].append(o)
    elif t == "sidebar":
        o = {"page": (S["pages"][0]["name"] if S["pages"] else "home"), "title": "",
             "icon": "", "area": "main", "role": "any", "order": 10}
        S["sidebar"].append(o)
    elif t == "setting":
        o = _s("field" + uid(), "新设置")
        S["settings"].append(o)
    elif t == "theme":
        if S["theme"]:
            self.set_status("主题是单例，已替换原有主题")
        S["theme"] = {"name": "theme-" + uid(), "label": "我的主题", "css": "theme.css",
                      "preview": "", "variables": {"--nebula-primary": "#22d3ee"}}
    elif t == "provider":
        S["providers"].append(_v("storage", "myprovider"))
    elif t == "customfile":
        S["customFiles"].append({"path": "extra.php", "content": "<?php\n// 自定义文件\n"})
    elif t.startswith("w-"):
        default = "1,024" if t == "w-stat" else ("提示信息" if t == "w-alert" else "标题文字")
        S["widgets"].append(_w(t, default, "说明文字"))
    else:
        return
    items = self.item_list()
    if t.startswith("w-") and self.cv != "page":
        self.cv = "page"
        self.cv_var.set("page")
        self.refresh_palette()
    items = self.item_list()
    self.sel = items[-1]["key"] if items else None
    self.changed(("canvas", "preview", "code", "check", "build", "inspect"))
    self.set_status("已添加控件：%s" % TYPE_LABEL.get(t, t))


# ---- 属性面板 ----
def app__field_set(self, obj, key, val, refresh=True):
    old = obj.get(key)
    if isinstance(old, int) and not isinstance(old, bool):
        try:
            val = int(str(val).strip())
        except (ValueError, TypeError):
            val = 0
    obj[key] = val
    if refresh:
        self.changed(("canvas", "preview", "code", "check", "build"))


def app__field(self, parent, obj, key, label, kind="text", options=None, help=""):
    row = tk.Frame(parent, bg=C["panel"])
    row.pack(fill="x", padx=10, pady=3)
    tk.Label(row, text=label, font=ui_font(9, bold=True), bg=C["panel"],
             fg=C["text"], anchor="w").pack(fill="x")
    value = obj.get(key, "")
    value = "" if value is None else value
    if kind == "bool":
        bv = tk.BooleanVar(value=bool(value))
        tk.Checkbutton(row, variable=bv, bg=C["panel"], activebackground=C["panel"],
                       command=lambda: self._field_set(obj, key, bv.get())).pack(anchor="w")
    elif kind == "select":
        var = tk.StringVar(value=str(value))
        cb = ttk.Combobox(row, textvariable=var, values=options or [], state="readonly",
                          font=ui_font(10))
        cb.pack(fill="x", pady=2)
        cb.bind("<<ComboboxSelected>>", lambda e: self._field_set(obj, key, var.get()))
    elif kind in ("textarea", "code"):
        box = tk.Text(row, height=5 if kind == "textarea" else 7, font=ui_font(9, mono=kind == "code"),
                      bg="#f8faff", fg=C["text"], relief="solid", bd=1, wrap="word")
        box.insert("1.0", str(value))
        box.pack(fill="x", pady=2)
        box.bind("<KeyRelease>", lambda e: self._field_set(obj, key, box.get("1.0", "end-1c")))
    else:
        var = tk.StringVar(value=str(value))
        ent = tk.Entry(row, textvariable=var, font=ui_font(10), bg="#f8faff",
                       fg=C["text"], relief="solid", bd=1)
        ent.pack(fill="x", pady=2, ipady=3)
        var.trace_add("write", lambda *a: self._field_set(obj, key, var.get()))
    if help:
        tk.Label(row, text=help, font=ui_font(8), bg=C["panel"], fg=C["muted"],
                 anchor="w", wraplength=270, justify="left").pack(fill="x")


def app_refresh_inspect(self):
    sf = self.inspect
    sf.clear()
    parent = sf.inner
    key = self.sel
    if not key:
        self.sel_name.configure(text="未选中")
        tk.Label(parent, text="点击画布中的卡片，\n或顶部的三个固定项，\n在这里编辑属性。",
                 font=ui_font(10), bg=C["panel"], fg=C["muted"], justify="left").pack(pady=24, padx=10)
        sf.rebind()
        return
    if key in ("meta", "runtime", "layout"):
        self._fixed_inspect(parent, key)
        sf.rebind()
        sf._on_inner()
        return
    it = self.find_item(key)
    if not it:
        self.sel_name.configure(text="该项已不存在")
        sf.rebind()
        return
    self.sel_name.configure(text=TYPE_LABEL.get(it["type"], it["type"]))
    o, T = it["obj"], it["type"]
    tk.Label(parent, text="◆ " + TYPE_LABEL.get(T, T), font=ui_font(10, bold=True),
             bg=C["panel"], fg=C["accent"], anchor="w").pack(fill="x", padx=10, pady=(6, 4))
    if T == "page":
        self._field(parent, o, "name", "页面标识 name *", help="只允许 [a-z0-9-_]，URL 末段")
        self._field(parent, o, "title", "侧边栏标题")
        self._field(parent, o, "home", "主页文件 home", help="相对 co.BEGG 所在目录，文件会自动生成")
        self._field(parent, o, "area", "区域 area", "select", AREAS, "main 主界面 / admin 后台 / public 公开")
        self._field(parent, o, "role", "可见角色 role", "select", ROLES)
        self._field(parent, o, "embed", "嵌入方式 embed", "select", EMBEDS)
        self._field(parent, o, "icon", "图标 icon", help="lucide 图标名，如 layout-dashboard")
        self._field(parent, o, "order", "排序 order", "number")
        self._field(parent, o, "url", "外部 URL url", help="填了就不反代，前端 iframe 直连（https://）")
        self._field(parent, o, "public", "public（宿主不鉴权，插件自负责）", "bool")
    elif T == "sidebar":
        names = [p["name"] for p in self.S["pages"]] or ["home"]
        self._field(parent, o, "page", "对应页面 page", "select", names)
        self._field(parent, o, "title", "标题 title")
        self._field(parent, o, "icon", "图标 icon")
        self._field(parent, o, "area", "区域 area", "select", AREAS)
        self._field(parent, o, "role", "角色 role", "select", ROLES)
        self._field(parent, o, "order", "排序 order", "number")
    elif T == "setting":
        self._field(parent, o, "key", "键 key *")
        self._field(parent, o, "label", "显示名 label")
        self._field(parent, o, "type", "类型 type", "select", SETTING_TYPES)
        self._field(parent, o, "default", "默认值 default")
        self._field(parent, o, "options", "选项 options", help="type=select 时，逗号分隔")
        self._field(parent, o, "help", "帮助文字 help")
    elif T == "theme":
        self._field(parent, o, "name", "标识 name")
        self._field(parent, o, "label", "显示名 label")
        self._field(parent, o, "css", "CSS 文件 css", help="相对 co.BEGG 目录，会自动生成一份示例")
        self._field(parent, o, "preview", "预览 preview")
        tk.Label(parent, text="CSS 变量（每行 键: 值）", font=ui_font(9, bold=True),
                 bg=C["panel"], fg=C["text"], anchor="w").pack(fill="x", padx=10, pady=(8, 2))
        vb = tk.Text(parent, height=5, font=ui_font(9, mono=True), bg="#f8faff",
                     relief="solid", bd=1)
        o["variables"] = o.get("variables") or {}
        vb.insert("1.0", "\n".join("%s: %s" % (k, v) for k, v in o["variables"].items()))
        vb.pack(fill="x", padx=10, pady=2)
        vb.bind("<KeyRelease>", lambda e: self._save_vars(o, vb))
        tk.Label(parent, text="常用：--nebula-primary / --nebula-accent / --nebula-bg",
                 font=ui_font(8), bg=C["panel"], fg=C["muted"], wraplength=270,
                 justify="left").pack(fill="x", padx=10)
    elif T == "provider":
        self._field(parent, o, "kind", "类别 kind", "select", PROVIDER_KINDS)
        self._field(parent, o, "name", "能力名 name *")
        self._field(parent, o, "label", "显示名 label")
        self._field(parent, o, "hook", "覆盖钩子 hook", help="留空使用宿主默认钩子")
    elif T == "hookgroup":
        tk.Label(parent, text="已订阅 %d 个钩子。\n订阅后宿主会拉起 JSON-RPC 可执行文件，\n制作器自动生成骨架。"
                 % len(self.S["hooks"]), font=ui_font(9), bg=C["panel"], fg=C["muted"],
                 justify="left").pack(fill="x", padx=10, pady=6)
        btn(parent, "打开 99 钩子选择器", self.open_hook_picker, kind="primary").pack(fill="x", padx=10, pady=4)
    elif T == "customfile":
        self._field(parent, o, "path", "路径 path", help="相对 co.BEGG 所在目录，如 lib.php / assets/app.js")
        self._field(parent, o, "content", "内容 content", "code", help="可写 PHP / HTML / CSS / JS")
    elif T.startswith("w-"):
        if T == "w-divider":
            tk.Label(parent, text="分隔线无需配置。", font=ui_font(9), bg=C["panel"],
                     fg=C["muted"]).pack(fill="x", padx=10, pady=8)
        elif T == "w-html":
            self._field(parent, o, "text", "HTML / PHP 片段", "code",
                        help="原样插入页面，可用 <?php ... ?> 输出动态内容")
            self._field(parent, o, "sub", "备注 sub")
        else:
            self._field(parent, o, "text", "占位文字" if T == "w-input" else "主文本 text")
            if T in ("w-heading", "w-card", "w-stat", "w-input"):
                self._field(parent, o, "sub", "副文本 sub")
    btn(parent, "删除此项", lambda: self.del_item(key), kind="danger").pack(fill="x", padx=10, pady=(14, 8))
    sf.rebind()
    sf._on_inner()


def app__save_vars(self, obj, textbox):
    d = {}
    for line in textbox.get("1.0", "end-1c").splitlines():
        if ":" in line:
            k, v = line.split(":", 1)
            if k.strip():
                d[k.strip()] = v.strip()
    obj["variables"] = d
    self.changed(("canvas", "preview", "code", "check", "build"))


def app__fixed_inspect(self, parent, which):
    if which == "meta":
        self.sel_name.configure(text="插件身份 · plugin.json")
        m = self.S["meta"]
        tk.Label(parent, text="◆ 插件身份", font=ui_font(10, bold=True), bg=C["panel"],
                 fg=C["accent"], anchor="w").pack(fill="x", padx=10, pady=(6, 4))
        self._field(parent, m, "name", "插件名 name *", help="2–64 字符，[a-z0-9-_]，不能以 -/_ 开头；也是目录名")
        self._field(parent, m, "title", "展示名 title")
        self._field(parent, m, "version", "版本 version", help="语义化 x.y.z")
        self._field(parent, m, "author", "作者 author")
        self._field(parent, m, "description", "描述 description", "textarea")
        self._field(parent, m, "protocolVersion", "协议 protocolVersion", "number", help="1 或 2")
        self._field(parent, m, "license", "许可证 license")
        self._field(parent, m, "homepage", "主页 homepage")
        self._field(parent, m, "minHostVersion", "最低宿主版本")
        self._field(parent, m, "permissions", "权限声明 permissions", help="逗号分隔，仅作记录与展示")
    elif which == "runtime":
        self.sel_name.configure(text="运行时 · co.BEGG")
        r, adv = self.S["runtime"], self.S["advanced"]
        tk.Label(parent, text="◆ 运行时（co.BEGG）", font=ui_font(10, bold=True), bg=C["panel"],
                 fg=C["accent"], anchor="w").pack(fill="x", padx=10, pady=(6, 4))
        self._field(parent, r, "runtime", "runtime *", "select", list(RUNTIMES))
        self._field(parent, r, "requires", "解释器要求 requires", help="逗号分隔：php>=7.4 / python3 / node>=18")
        self._field(parent, r, "home", "主页 home", help="相对 co.BEGG 所在目录；文件会自动生成")
        self._field(parent, r, "entry", "入口脚本 entry", help="python/node 的服务脚本；纯页面 PHP 可留空")
        self._field(parent, r, "static", "静态根 static", help="留空取 home 所在目录")
        self._field(parent, r, "listen", "监听 listen", help="0.0.0.0 = 按后台绑定域名/IP 替换；也可写 https:// 外部地址")
        self._field(parent, r, "port", "端口 port", "number", help="0 = 宿主动态分配")
        self._field(parent, r, "command", "启动命令 command", help="占位符 {port} {host} {dir} {entry} {home} {static}")
        tk.Label(parent, text="◆ 高级 · JSON-RPC 主体", font=ui_font(10, bold=True),
                 bg=C["panel"], fg=C["accent"], anchor="w").pack(fill="x", padx=10, pady=(12, 4))
        self._field(parent, adv, "jsonrpc", "生成钩子处理骨架文件", "bool")
        self._field(parent, adv, "lang", "骨架语言", "select", ["python", "node"])
        tk.Label(parent, text="订阅钩子时会自动开启。entry 相对插件根目录，Linux 需 chmod +x。",
                 font=ui_font(8), bg=C["panel"], fg=C["muted"], wraplength=270,
                 justify="left").pack(fill="x", padx=10)
    else:
        self.sel_name.configure(text="目录布局")
        tk.Label(parent, text="◆ 目录布局", font=ui_font(10, bold=True), bg=C["panel"],
                 fg=C["accent"], anchor="w").pack(fill="x", padx=10, pady=(6, 4))
        row = tk.Frame(parent, bg=C["panel"])
        row.pack(fill="x", padx=10, pady=4)
        tk.Label(row, text="三文件与页面放在哪", font=ui_font(9, bold=True), bg=C["panel"],
                 fg=C["text"], anchor="w").pack(fill="x")
        var = tk.StringVar(value=self.S["dirLayout"])
        cb = ttk.Combobox(row, textvariable=var, values=["flat", "ass"], state="readonly", font=ui_font(10))
        cb.pack(fill="x", pady=2)
        cb.bind("<<ComboboxSelected>>",
                lambda e: (self.S.__setitem__("dirLayout", var.get()),
                           self.changed(("canvas", "preview", "code", "check", "build"))))
        tk.Label(parent, text="flat = 全部在插件根目录\nass  = plugin.json 在根目录，\n        register.go / co.BEGG / 页面在 ASS/ 子目录",
                 font=ui_font(9), bg=C["panel"], fg=C["muted"], justify="left").pack(fill="x", padx=10, pady=6)
        tk.Label(parent, text="宿主递归定位三文件（深度 ≤ 5），目录形式不限定。\n"
                              "相对路径基准始终是 co.BEGG 所在目录：\nhome 写 in.php，不要写 ASS/in.php。",
                 font=ui_font(8), bg="#fff8e6", fg="#8a5b00", justify="left",
                 wraplength=272, padx=8, pady=8).pack(fill="x", padx=10, pady=6)


def app_open_hook_picker(self):
    win = tk.Toplevel(self)
    win.title("钩子订阅 · 共 %d 个" % len(HOOKS))
    win.configure(bg=C["bg"])
    win.geometry("760x620")
    win.transient(self)
    head = tk.Frame(win, bg=C["dark"])
    head.pack(fill="x")
    tk.Label(head, text="勾选后自动写入 plugin.json 与 register.go（两边集合保持一致）",
             font=ui_font(9), bg=C["dark"], fg="#c9d6ff", anchor="w").pack(fill="x", padx=14, pady=(10, 2))
    bar = tk.Frame(head, bg=C["dark"])
    bar.pack(fill="x", padx=14, pady=(0, 10))
    self.hook_q = tk.StringVar()
    ent = tk.Entry(bar, textvariable=self.hook_q, font=ui_font(10), width=32, relief="flat", bd=1)
    ent.pack(side="left", ipady=4)
    tk.Label(bar, text="　同步=可拦截/变换决策点，异步=纯通知", font=ui_font(9),
             bg=C["dark"], fg="#9fb0e0").pack(side="left")
    sf = ScrollFrame(win)
    sf.pack(fill="both", expand=True, padx=10, pady=8)
    self.hook_vars = {h["name"]: tk.BooleanVar(value=h["name"] in self.S["hooks"]) for h in HOOKS}
    self.hook_rows = []
    for g in HOOK_GROUPS:
        gh = tk.Frame(sf.inner, bg="#e8efff")
        gh.pack(fill="x", pady=(8, 2))
        tk.Label(gh, text="  %s · %d 个" % (g["label"], len(g["items"])),
                 font=ui_font(10, bold=True), bg="#e8efff", fg=C["accent"],
                 anchor="w").pack(fill="x", pady=3)
        for h in g["items"]:
            row = tk.Frame(sf.inner, bg=C["panel"])
            row.pack(fill="x", padx=2, pady=1)
            cb = tk.Checkbutton(row, variable=self.hook_vars[h["name"]], bg=C["panel"],
                                activebackground=C["panel"], command=self._hook_count)
            cb.pack(side="left")
            tk.Label(row, text=h["name"], font=ui_font(9, mono=True), bg=C["panel"],
                     fg=C["text"], width=26, anchor="w").pack(side="left")
            badges = []
            if h["mode"] == "sync":
                badges.append("同步")
            if h["blockable"]:
                badges.append("可拦截")
            if h["mutatable"]:
                badges.append("可变换")
            if h["since"] == 1:
                badges.append("v1")
            tk.Label(row, text=" ".join("[" + b + "]" for b in badges), font=ui_font(8),
                     bg=C["panel"], fg=C["accent2"], width=18, anchor="w").pack(side="left")
            tk.Label(row, text=h["doc"], font=ui_font(8), bg=C["panel"], fg=C["muted"],
                     anchor="w", justify="left", wraplength=390).pack(side="left", fill="x", expand=True)
            self.hook_rows.append((row, h))
    self.hook_q.trace_add("write", lambda *a: self._filter_hooks(sf))
    foot = tk.Frame(win, bg=C["bg"])
    foot.pack(fill="x", padx=12, pady=10)
    self.hook_count_lb = tk.Label(foot, text="", font=ui_font(10, bold=True), bg=C["bg"], fg=C["text"])
    self.hook_count_lb.pack(side="left")
    btn(foot, "全选可见", self._hook_select_visible, kind="normal").pack(side="left", padx=6)
    btn(foot, "清空", self._hook_clear, kind="normal").pack(side="left", padx=6)
    btn(foot, "取消", win.destroy).pack(side="right", padx=6)

    def save():
        self.S["hooks"] = [h["name"] for h in HOOKS if self.hook_vars[h["name"]].get()]
        if self.S["hooks"]:
            self.S["advanced"]["jsonrpc"] = True
        win.destroy()
        self.sel = None
        self.changed(("canvas", "preview", "code", "check", "build", "inspect"))
        self.set_status("已保存 %d 个钩子订阅" % len(self.S["hooks"]))
    btn(foot, "保存订阅", save, kind="primary").pack(side="right", padx=6)
    self._hook_count()
    sf.rebind()
    sf._on_inner()


def app__hook_count(self):
    n = sum(1 for v in self.hook_vars.values() if v.get())
    if hasattr(self, "hook_count_lb"):
        self.hook_count_lb.configure(text="已勾选 %d 个钩子" % n)


def app__filter_hooks(self, sf):
    q = (self.hook_q.get() or "").lower()
    for row, h in self.hook_rows:
        text = (h["name"] + " " + h["doc"] + " " + h["groupLabel"]).lower()
        if q in text:
            row.pack(fill="x", padx=2, pady=1)
        else:
            row.pack_forget()
    sf._on_inner()


def app__hook_select_visible(self):
    q = (self.hook_q.get() or "").lower()
    for h in HOOKS:
        if q in (h["name"] + " " + h["doc"] + " " + h["groupLabel"]).lower():
            self.hook_vars[h["name"]].set(True)
    self._hook_count()


def app__hook_clear(self):
    for v in self.hook_vars.values():
        v.set(False)
    self._hook_count()


# ---------------------------------------------------------------------------
# 十、代码 / 校验 / 构建 / 预览 页
# ---------------------------------------------------------------------------

def app__build_code_tab(self):
    top = tk.Frame(self.tab_code, bg=C["bg"])
    top.pack(fill="x", padx=8, pady=8)
    tk.Label(top, text="生成的文件可直接编辑，改动自动写回工程；● 表示手工编辑过的文件。",
             font=ui_font(9), bg=C["bg"], fg=C["muted"]).pack(side="left")
    btn(top, "在浏览器预览页面", self.on_browser_preview).pack(side="right", padx=3)
    btn(top, "重置为自动生成", self.on_code_reset).pack(side="right", padx=3)
    btn(top, "同步回设计", self.on_code_sync).pack(side="right", padx=3)

    body = tk.Frame(self.tab_code, bg=C["bg"])
    body.pack(fill="both", expand=True, padx=8, pady=(0, 8))
    left = tk.Frame(body, bg=C["panel"], width=230)
    left.pack(side="left", fill="y")
    left.pack_propagate(False)
    tk.Label(left, text="文件列表", font=ui_font(10, bold=True), bg=C["panel"],
             fg=C["text"], anchor="w").pack(fill="x", padx=8, pady=(8, 3))
    self.code_list = tk.Listbox(left, font=ui_font(9, mono=True), bg="#f8faff",
                                fg=C["text"], relief="flat", bd=0,
                                selectbackground=C["accent"], selectforeground="#ffffff",
                                activestyle="none", exportselection=False)
    self.code_list.pack(fill="both", expand=True, padx=6, pady=(0, 8))
    self.code_list.bind("<<ListboxSelect>>", self.on_code_file_selected)
    self.code_status = tk.Label(left, text="", font=ui_font(8), bg=C["panel"],
                                fg=C["muted"], wraplength=214, justify="left", anchor="w")
    self.code_status.pack(fill="x", padx=8, pady=(0, 8))

    right = tk.Frame(body, bg=C["panel"], highlightbackground=C["line"], highlightthickness=1)
    right.pack(side="left", fill="both", expand=True, padx=(8, 0))
    self.code_text = scrolledtext.ScrolledText(right, font=ui_font(10, mono=True),
                                               bg="#0f1630", fg="#dce6ff", insertbackground="#ffffff",
                                               relief="flat", bd=0, wrap="none", undo=True)
    self.code_text.pack(fill="both", expand=True, padx=2, pady=2)
    self.code_text.bind("<KeyRelease>", lambda e: self._sync_code_text())
    self._code_loading = False


def app_refresh_code(self):
    f = final_files(self.S)
    names = list(f.keys())
    self.code_names = names
    if self.code_file not in names:
        self.code_file = names[0] if names else "plugin.json"
    self._code_loading = True
    self.code_list.delete(0, "end")
    for n in names:
        mark = "● " if (self.S.get("codeOverride") or {}).get(n) is not None else "  "
        self.code_list.insert("end", mark + n)
    i = names.index(self.code_file) if self.code_file in names else 0
    self.code_list.selection_clear(0, "end")
    self.code_list.selection_set(i)
    self.code_list.see(i)
    content = f.get(self.code_file, "")
    self.code_text.delete("1.0", "end")
    self.code_text.insert("1.0", content)
    self._code_loading = False
    ov = (self.S.get("codeOverride") or {}).get(self.code_file)
    self.code_status.configure(text=("手工编辑中 · 构建时将覆盖自动生成内容" if ov is not None
                                     else "自动生成 · 与宿主 v2 契约一致"))


def app_on_code_file_selected(self, e=None):
    if getattr(self, "_code_loading", False):
        return
    sel = self.code_list.curselection()
    if not sel:
        return
    self.code_file = self.code_names[sel[0]]
    self.refresh_code()


def app__sync_code_text(self):
    if getattr(self, "_code_loading", False):
        return
    cur = self.code_text.get("1.0", "end-1c")
    auto = build_files(self.S).get(self.code_file, "")
    ov = self.S.setdefault("codeOverride", {})
    if cur == auto:
        ov.pop(self.code_file, None)
    else:
        ov[self.code_file] = cur
    self._schedule_save()
    self.refresh_check()
    self.refresh_build()
    self.code_status.configure(text="手工编辑中 · 构建时将覆盖自动生成内容")


def app_on_code_reset(self):
    ov = self.S.setdefault("codeOverride", {})
    ov.pop(self.code_file, None)
    self.refresh_code()
    self.refresh_check()
    self.refresh_build()
    self.set_status("已重置 %s 为自动生成内容" % self.code_file)


def app_on_code_sync(self):
    ov = self.S.get("codeOverride") or {}
    val = ov.get(self.code_file)
    if val is None:
        messagebox.showinfo("同步回设计", "当前文件没有手工改动，无需同步。")
        return
    sub = "ASS/" if self.S["dirLayout"] == "ass" else ""
    try:
        o = json.loads(val)
        if self.code_file == "plugin.json":
            m = self.S["meta"]
            for k in ("name", "title", "version", "author", "description", "license",
                      "homepage", "minHostVersion", "protocolVersion"):
                if k in o:
                    m[k] = o[k]
            if "permissions" in o:
                m["permissions"] = ",".join(o["permissions"]) if isinstance(o["permissions"], list) else o["permissions"]
            if "hooks" in o:
                self.S["hooks"] = list(o["hooks"])
            if "pages" in o:
                self.S["pages"] = []
                for p in o["pages"]:
                    base = _p(p.get("name", ""), p.get("title", ""), p.get("home", ""),
                              p.get("area", "main"))
                    base.update(p)
                    self.S["pages"].append(base)
            if "providers" in o:
                self.S["providers"] = list(o["providers"])
            if o.get("entry"):
                self.S["advanced"]["jsonrpc"] = True
                if not self.S["hooks"]:
                    self.S["hooks"] = ["onBoot"]
        elif self.code_file == sub + "co.BEGG":
            r = self.S["runtime"]
            for k in ("runtime", "entry", "home", "static", "listen", "port", "command"):
                if k in o:
                    r[k] = o[k]
            if "requires" in o:
                r["requires"] = ",".join(o["requires"]) if isinstance(o["requires"], list) else o["requires"]
            if "env" in o:
                r["env"] = o["env"]
            ui = o.get("ui") or {}
            if "sidebar" in ui:
                self.S["sidebar"] = list(ui["sidebar"])
            if "settings" in ui:
                self.S["settings"] = list(ui["settings"])
            if "theme" in ui:
                self.S["theme"] = ui["theme"]
            if "pages" in o and not self.S["pages"]:
                self.S["pages"] = []
                for p in o["pages"]:
                    base = _p(p.get("name", ""), p.get("title", ""), p.get("home", ""), p.get("area", "main"))
                    base.update(p)
                    self.S["pages"].append(base)
        else:
            messagebox.showinfo("同步回设计", "该文件保留手工内容，不做反向解析（避免破坏设计结构）。")
            return
        ov.pop(self.code_file, None)
        self.sel = None
        self.changed(("canvas", "preview", "code", "check", "build", "inspect"))
        self.set_status("%s 已同步回设计" % self.code_file)
    except Exception as e:
        messagebox.showerror("同步失败", "%s\n\n请检查 JSON 语法。" % e)


def app__build_check_tab(self):
    top = tk.Frame(self.tab_check, bg=C["bg"])
    top.pack(fill="x", padx=10, pady=8)
    self.check_summary = tk.Label(top, text="", font=ui_font(12, bold=True), bg=C["bg"], fg=C["text"])
    self.check_summary.pack(side="left")
    btn(top, "重新校验", self.refresh_check).pack(side="right", padx=3)
    btn(top, "去构建", lambda: self.nb.select(self.tab_build)).pack(side="right", padx=3)

    box = tk.Frame(self.tab_check, bg=C["panel"], highlightbackground=C["line"], highlightthickness=1)
    box.pack(fill="both", expand=True, padx=10, pady=(0, 10))
    cols = ("状态", "项目", "说明")
    self.check_tree = ttk.Treeview(box, columns=cols, show="headings", selectmode="browse")
    for c, w in zip(cols, (70, 230, 760)):
        self.check_tree.heading(c, text=c)
        self.check_tree.column(c, width=w, anchor="w")
    vs = ttk.Scrollbar(box, orient="vertical", command=self.check_tree.yview)
    self.check_tree.configure(yscrollcommand=vs.set)
    self.check_tree.pack(side="left", fill="both", expand=True)
    vs.pack(side="right", fill="y")
    self.check_tree.tag_configure("ok", foreground=C["ok"])
    self.check_tree.tag_configure("bad", foreground=C["bad"])
    self.check_tree.tag_configure("warn", foreground=C["warn"])


def app_refresh_check(self):
    if not hasattr(self, "check_tree"):
        return
    r = validate(self.S)
    self.check_tree.delete(*self.check_tree.get_children())
    for x in r:
        tag = "warn" if x["warn"] else ("ok" if x["ok"] else "bad")
        st = "!" if x["warn"] else ("✓" if x["ok"] else "✕")
        self.check_tree.insert("", "end", values=(st, x["name"], x["detail"]), tags=(tag,))
    bad = [x for x in r if not x["ok"] and not x["warn"]]
    warn = [x for x in r if x["warn"]]
    self.check_summary.configure(
        text=("✕ 有 %d 项未通过，构建已阻止" % len(bad)) if bad else
             ("✓ 全部 %d 项核心校验通过" % (len(r) - len(warn))),
        fg=C["bad"] if bad else C["ok"])
    self.refresh_status()


def app__build_build_tab(self):
    top = tk.Frame(self.tab_build, bg=C["bg"])
    top.pack(fill="x", padx=8, pady=8)
    tk.Label(top, text="构建预览 · 目录树与每个文件的内容", font=ui_font(10, bold=True),
             bg=C["bg"], fg=C["text"]).pack(side="left")
    btn(top, "导出到目录 …", self.on_build_dir).pack(side="right", padx=3)
    btn(top, "一键构建 ZIP", self.on_build_zip, kind="primary").pack(side="right", padx=3)

    body = tk.Frame(self.tab_build, bg=C["bg"])
    body.pack(fill="both", expand=True, padx=8, pady=(0, 4))
    left = tk.Frame(body, bg=C["panel"], width=330)
    left.pack(side="left", fill="y")
    left.pack_propagate(False)
    tk.Label(left, text="目录树", font=ui_font(9, bold=True), bg=C["panel"], fg=C["text"],
             anchor="w").pack(fill="x", padx=8, pady=(8, 2))
    self.tree_text = tk.Text(left, height=9, font=ui_font(9, mono=True), bg="#0f1630",
                             fg="#c8d6ff", relief="flat", bd=0, wrap="none")
    self.tree_text.pack(fill="x", padx=6, pady=(0, 6))
    self.build_list = tk.Listbox(left, font=ui_font(9, mono=True), bg="#f8faff", fg=C["text"],
                                 relief="flat", bd=0, selectbackground=C["accent"],
                                 selectforeground="#ffffff", activestyle="none", exportselection=False)
    self.build_list.pack(fill="both", expand=True, padx=6, pady=(0, 8))
    self.build_list.bind("<<ListboxSelect>>", self.on_build_file_selected)

    right = tk.Frame(body, bg=C["panel"], highlightbackground=C["line"], highlightthickness=1)
    right.pack(side="left", fill="both", expand=True, padx=(8, 0))
    self.build_text = scrolledtext.ScrolledText(right, font=ui_font(9, mono=True),
                                                bg="#0f1630", fg="#dce6ff", relief="flat",
                                                bd=0, wrap="none")
    self.build_text.pack(fill="both", expand=True, padx=2, pady=2)
    self.build_text.configure(state="disabled")

    note = tk.Label(self.tab_build, text=(
        "⚠ 订阅钩子会拉起 plugin.py 做 JSON-RPC（Linux 需 chmod +x，Windows 需改成 .exe）；"
        "宿主对 /ext/* 完全透传鉴权，插件页面必须自己鉴权；"
        "插件默认关闭，需先在后台开启全局总开关。"),
        font=ui_font(9), bg="#fff8e6", fg="#8a5b00", justify="left", wraplength=1180,
        anchor="w", padx=10, pady=7)
    note.pack(fill="x", padx=8, pady=(0, 8))


def app_refresh_build(self):
    if not hasattr(self, "build_list"):
        return
    f = final_files(self.S)
    names = list(f.keys())
    self.build_names = names
    sub = "ASS/" if self.S["dirLayout"] == "ass" else ""
    lines = [self.S["meta"]["name"] + "/"]
    if sub:
        root_keys = [n for n in names if not n.startswith(sub)]
        inner = [n for n in names if n.startswith(sub)]
        for i, n in enumerate(root_keys):
            lines.append("%s %s  (%d B)" % ("├─" if i < len(root_keys) - 1 else "└─", n, len(f[n].encode("utf-8"))))
        lines.append("└─ ASS/   ← co.BEGG 所在目录 = 相对路径基准")
        for i, n in enumerate(inner):
            lines.append("    %s %s  (%d B)" % ("├─" if i < len(inner) - 1 else "└─",
                                                n[len(sub):], len(f[n].encode("utf-8"))))
    else:
        for i, n in enumerate(names):
            lines.append("%s %s  (%d B)" % ("├─" if i < len(names) - 1 else "└─", n, len(f[n].encode("utf-8"))))
    self.tree_text.configure(state="normal")
    self.tree_text.delete("1.0", "end")
    self.tree_text.insert("1.0", "\n".join(lines))
    self.tree_text.configure(state="disabled")

    self.build_list.delete(0, "end")
    for n in names:
        self.build_list.insert("end", n)
    if self.build_sel not in names:
        self.build_sel = names[0] if names else None
    if self.build_sel:
        i = names.index(self.build_sel)
        self.build_list.selection_clear(0, "end")
        self.build_list.selection_set(i)
        self.build_list.see(i)
        self.build_text.configure(state="normal")
        self.build_text.delete("1.0", "end")
        self.build_text.insert("1.0", f[self.build_sel])
        self.build_text.configure(state="disabled")


def app_on_build_file_selected(self, e=None):
    sel = self.build_list.curselection()
    if not sel:
        return
    self.build_sel = self.build_names[sel[0]]
    self.refresh_build()


def app__build_preview_tab(self):
    top = tk.Frame(self.tab_preview, bg=C["bg"])
    top.pack(fill="x", padx=8, pady=8)
    tk.Label(top, text="模拟网盘中的插件页面（iframe /ext/<插件名>/）· 控件实时渲染",
             font=ui_font(9), bg=C["bg"], fg=C["muted"]).pack(side="left")
    btn(top, "在浏览器打开完整预览", self.on_browser_preview, kind="primary").pack(side="right")
    self.preview_sf = ScrollFrame(self.tab_preview, bg=C["bg"])
    self.preview_sf.pack(fill="both", expand=True, padx=8, pady=(0, 8))


def app_on_browser_preview(self):
    s = self.S
    f = final_files(s)
    home = s["runtime"].get("home") or (s["pages"][0].get("home") if s["pages"] else "")
    sub = "ASS/" if s["dirLayout"] == "ass" else ""
    content = f.get(sub + home) or f.get(home) or gen_page_html(s)
    p = Path(tempfile.gettempdir()) / "nebuladrive_plugin_preview.html"
    try:
        p.write_text(content, encoding="utf-8")
        webbrowser.open(p.as_uri())
        self.set_status("已在浏览器打开预览（PHP 片段不会执行，仅展示 HTML 结构）")
    except Exception as e:
        messagebox.showerror("预览失败", str(e))


def app__widget_widget(self, parent, w):
    t, text, sub = w.get("t"), w.get("text", ""), w.get("sub", "")
    dark, dark2, fg, muted, line = C["dark"], C["dark2"], "#e6ecff", "#8d9cc4", C["darkline"]
    if t == "w-heading":
        tk.Label(parent, text=text, font=ui_font(16, bold=True), bg=dark, fg=fg,
                 anchor="w").pack(fill="x", pady=(0, 2))
        if sub:
            tk.Label(parent, text=sub, font=ui_font(9), bg=dark, fg=muted, anchor="w").pack(fill="x")
    elif t == "w-text":
        tk.Label(parent, text=text, font=ui_font(10), bg=dark, fg="#c8d4f5",
                 anchor="w", wraplength=560, justify="left").pack(fill="x")
    elif t == "w-card":
        f = tk.Frame(parent, bg=dark2, highlightbackground=line, highlightthickness=1)
        f.pack(fill="x", pady=2)
        tk.Label(f, text=text, font=ui_font(10, bold=True), bg=dark2, fg=fg,
                 anchor="w").pack(fill="x", padx=12, pady=(9, 0))
        tk.Label(f, text=sub, font=ui_font(9), bg=dark2, fg=muted,
                 anchor="w").pack(fill="x", padx=12, pady=(0, 9))
    elif t == "w-stat":
        tk.Label(parent, text=text, font=ui_font(24, bold=True), bg=dark,
                 fg=C["accent2"], anchor="w").pack(fill="x")
        tk.Label(parent, text=sub, font=ui_font(9), bg=dark, fg=muted, anchor="w").pack(fill="x")
    elif t == "w-button":
        tk.Button(parent, text=text, font=ui_font(9, bold=True), bg=C["accent"],
                  fg="#ffffff", relief="flat", bd=0, padx=14, pady=5,
                  cursor="hand2").pack(anchor="w", pady=2)
    elif t == "w-input":
        tk.Label(parent, text=sub or text, font=ui_font(9), bg=dark, fg=muted,
                 anchor="w").pack(fill="x")
        tk.Entry(parent, font=ui_font(10), bg="#0b1020", fg=fg, relief="solid", bd=1,
                 insertbackground="#ffffff").pack(fill="x", ipady=4, pady=2)
    elif t == "w-table":
        tk.Label(parent, text=text, font=ui_font(10, bold=True), bg=dark, fg=fg,
                 anchor="w").pack(fill="x")
        grid = tk.Frame(parent, bg=line)
        grid.pack(fill="x", pady=(4, 2))
        data = [["项目", "值", "状态"], ["示例 A", "128", "正常"], ["示例 B", "64", "正常"]]
        for r, row in enumerate(data):
            for c, cell in enumerate(row):
                tk.Label(grid, text=cell, font=ui_font(9), bg=dark2 if r == 0 else dark,
                         fg=muted if r == 0 else fg, anchor="w", padx=8, pady=4,
                         relief="solid", bd=1 if False else 0).grid(row=r, column=c, sticky="ew", padx=1, pady=1)
            grid.grid_columnconfigure(0, weight=1)
            grid.grid_columnconfigure(1, weight=1)
            grid.grid_columnconfigure(2, weight=1)
    elif t == "w-alert":
        tk.Label(parent, text="⚠  " + text, font=ui_font(9), bg="#3a2f10", fg="#fbbf24",
                 anchor="w", wraplength=560, justify="left", padx=10, pady=8).pack(fill="x")
    elif t == "w-divider":
        tk.Frame(parent, bg=line, height=1).pack(fill="x", pady=8)
    elif t == "w-html":
        tk.Label(parent, text=text[:400], font=ui_font(9, mono=True), bg="#0b1020",
                 fg="#9fb4e8", anchor="w", justify="left", wraplength=560,
                 padx=8, pady=6).pack(fill="x")
    tk.Frame(parent, bg=dark, height=8).pack(fill="x")


def app_refresh_preview(self):
    if not hasattr(self, "preview_sf"):
        return
    sf = self.preview_sf
    sf.clear()
    p = sf.inner
    S = self.S
    shell = tk.Frame(p, bg=C["dark"], highlightbackground=C["darkline"], highlightthickness=1)
    shell.pack(fill="both", expand=True, padx=6, pady=6)
    bar = tk.Frame(shell, bg="#1a2342", height=30)
    bar.pack(fill="x")
    bar.pack_propagate(False)
    for col in ("#f87171", "#fbbf24", "#34d399"):
        tk.Label(bar, text="●", font=ui_font(8), bg="#1a2342", fg=col).pack(side="left", padx=(6, 0))
    tk.Label(bar, text="  NebulaDrive · 插件页面预览", font=ui_font(9), bg="#1a2342",
             fg="#c8d4f5").pack(side="left")
    tk.Label(bar, text="iframe /ext/%s/   " % S["meta"]["name"], font=ui_font(8),
             bg="#1a2342", fg="#8d9cc4").pack(side="right")

    main = tk.Frame(shell, bg=C["dark"])
    main.pack(fill="both", expand=True)
    side = tk.Frame(main, bg="#0b1020", width=170)
    side.pack(side="left", fill="y")
    side.pack_propagate(False)
    tk.Label(side, text="主界面", font=ui_font(8, bold=True), bg="#0b1020", fg="#6b7699",
             anchor="w").pack(fill="x", padx=10, pady=(12, 4))
    for t in ("▤ 文件", "▦ 分享"):
        tk.Label(side, text=t, font=ui_font(9), bg="#0b1020", fg="#a9b7dd",
                 anchor="w").pack(fill="x", padx=10, pady=2)
    mp = [x for x in S["pages"] if x.get("area") == "main"]
    ap = [x for x in S["pages"] if x.get("area") == "admin"]
    if mp:
        tk.Label(side, text="插件 · 主界面", font=ui_font(8, bold=True), bg="#0b1020",
                 fg="#6b7699", anchor="w").pack(fill="x", padx=10, pady=(12, 4))
        for x in mp:
            tk.Label(side, text="◈ " + (x.get("title") or x.get("name")), font=ui_font(9),
                     bg="#182248", fg=C["accent2"], anchor="w", padx=6, pady=3).pack(fill="x", padx=6, pady=1)
    tk.Label(side, text="后台", font=ui_font(8, bold=True), bg="#0b1020", fg="#6b7699",
             anchor="w").pack(fill="x", padx=10, pady=(14, 4))
    tk.Label(side, text="⚙ 设置", font=ui_font(9), bg="#0b1020", fg="#a9b7dd",
             anchor="w").pack(fill="x", padx=10, pady=2)
    if ap:
        tk.Label(side, text="插件 · 后台", font=ui_font(8, bold=True), bg="#0b1020",
                 fg="#6b7699", anchor="w").pack(fill="x", padx=10, pady=(12, 4))
        for x in ap:
            tk.Label(side, text="◈ " + (x.get("title") or x.get("name")), font=ui_font(9),
                     bg="#182248", fg=C["accent2"], anchor="w", padx=6, pady=3).pack(fill="x", padx=6, pady=1)

    content = tk.Frame(main, bg=C["dark"])
    content.pack(side="left", fill="both", expand=True, padx=18, pady=14)
    if S["widgets"]:
        for w in S["widgets"]:
            self._widget_widget(content, w)
    else:
        tk.Label(content, text="插件主页预览\n\n在「① 设计」切到页面画布，拖入控件即可",
                 font=ui_font(11), bg=C["dark"], fg="#8d9cc4", justify="left").pack(anchor="w", pady=24)
    if S["settings"]:
        tk.Frame(content, bg=C["darkline"], height=1).pack(fill="x", pady=8)
        tk.Label(content, text="插件设置面板预览", font=ui_font(9), bg=C["dark"],
                 fg="#8d9cc4", anchor="w").pack(fill="x")
        for x in S["settings"]:
            row = tk.Frame(content, bg=C["dark"])
            row.pack(fill="x", pady=2)
            tk.Label(row, text=x.get("label") or x.get("key"), font=ui_font(9), bg=C["dark"],
                     fg="#c8d4f5", width=16, anchor="w").pack(side="left")
            if x.get("type") == "bool":
                tk.Checkbutton(row, bg=C["dark"], activebackground=C["dark"]).pack(side="left")
            elif x.get("type") == "select":
                cb = ttk.Combobox(row, values=(x.get("options") or "").split(","),
                                  state="readonly", width=16, font=ui_font(9))
                cb.set((x.get("options") or "").split(",")[0] if x.get("options") else "")
                cb.pack(side="left")
            else:
                tk.Entry(row, width=24, font=ui_font(9), bg="#0b1020", fg="#e6ecff",
                         relief="solid", bd=1, insertbackground="#ffffff").pack(side="left", ipady=2)
    sf.rebind()
    sf._on_inner()


# ---------------------------------------------------------------------------
# 十一、命令行：自检 / 批量导出 / 启动窗口
# ---------------------------------------------------------------------------

def app_run_gui_check(self):
    """无人工干预的 GUI 交互自检：遍历模板、拖控件、改属性、开弹窗、编辑代码。"""
    results = []

    def step(name, fn):
        try:
            fn()
            results.append((True, name))
        except Exception as e:
            results.append((False, "%s -> %s: %s" % (name, type(e).__name__, e)))

    def close_popups():
        for w in list(self.winfo_children()):
            if isinstance(w, tk.Toplevel):
                w.destroy()

    def check_ok():
        bad = [x["name"] for x in validate(self.S) if not x["ok"] and not x["warn"]]
        if bad:
            raise AssertionError("校验未通过: " + ", ".join(bad))

    close_popups()
    step("关闭已有弹窗", lambda: None)
    for t in TEMPLATES:
        k = t["key"]
        step("加载模板 " + k, lambda k=k: self.load_template(k))
        step("五页签刷新 " + k, lambda: [self.nb.select(x) for x in
             (self.tab_code, self.tab_check, self.tab_build, self.tab_preview, self.tab_design)])
        step("契约校验 " + k, check_ok)

    self.load_template("blank")
    step("切换到页面画布", lambda: (setattr(self, "cv", "page"),
                                     self.refresh_palette(), self.refresh_canvas()))
    for ctl, _name, _desc in WIDGET_CONTROLS:
        step("拖入页面控件 " + ctl, lambda c=ctl: self.add_control(c))
    step("选中画布卡片", lambda: self.select_item(self.item_list()[-1]["key"]))
    step("卡片上移", lambda: self.move_item(self.item_list()[0]["key"], 1))
    step("卡片下移", lambda: self.move_item(self.item_list()[-1]["key"], -1))
    step("删除卡片", lambda: self.del_item(self.item_list()[-1]["key"]))
    step("切换到结构画布", lambda: (setattr(self, "cv", "struct"),
                                     self.refresh_palette(), self.refresh_canvas()))
    for ctl, *_rest in STRUCT_CONTROLS:
        if ctl == "hook":
            continue
        step("拖入结构控件 " + ctl, lambda c=ctl: self.add_control(c))
    for fixed in ("meta", "runtime", "layout"):
        step("固定属性 " + fixed, lambda f=fixed: self.select_item(f))

    def hook_step():
        self.open_hook_picker()
        self._hook_select_visible()
        self._hook_clear()
        close_popups()
    step("99 钩子选择器", hook_step)

    def code_step():
        self.nb.select(self.tab_code)
        self.refresh_code()
        self.code_text.insert("end", "\n// 手工编辑自检\n")
        self._sync_code_text()
        assert self.S["codeOverride"].get(self.code_file) is not None
        self.on_code_reset()
    step("代码页编辑 / 重置", code_step)
    step("构建页目录树", lambda: (self.nb.select(self.tab_build), self.refresh_build()))
    step("预览页控件渲染", lambda: (self.nb.select(self.tab_preview), self.refresh_preview()))

    def tutorial_step():
        w = Tutorial(self, self.on_tutorial_done)
        w.next_step()
        w.prev_step()
        w.finish()
        close_popups()
    step("新手教程 6 步", tutorial_step)

    ok = all(x[0] for x in results)
    print("GUI 交互自检：%d 项，%s" % (len(results), "全部通过 ✓" if ok else "存在失败 ✕"))
    for good, name in results:
        if not good:
            print("  ✕ " + name)
    self._gui_check_ok = ok
    self.after(30, self.on_close)
    return ok


def run_selftest(verbose=True):
    lines = []
    all_ok = True
    for t in TEMPLATES:
        s = apply_template(t["key"])
        problems = []
        try:
            files = build_files(s)
            r = validate(s)
            bad = [x["name"] for x in r if not x["ok"] and not x["warn"]]
            if bad:
                problems.append("校验未通过: " + ", ".join(bad))
            with tempfile.TemporaryDirectory() as td:
                zp = os.path.join(td, t["key"] + ".zip")
                n = make_zip(s, zp)
                with zipfile.ZipFile(zp) as z:
                    err = z.testzip()
                    if err is not None:
                        problems.append("ZIP CRC 损坏: " + err)
                    names = z.namelist()
                    for need in ("plugin.json", "register.go", "co.BEGG"):
                        if not any(x.endswith("/" + need) for x in names):
                            problems.append("缺文件 " + need)
                    if len(names) != n:
                        problems.append("ZIP 文件数不符")
                root, cnt = write_dir(s, td)
                for need in ("plugin.json", "register.go", "co.BEGG"):
                    found = list(Path(root).rglob(need))
                    if not found:
                        problems.append("导出目录缺 " + need)
        except Exception as e:
            problems.append("异常: %s: %s" % (type(e).__name__, e))
        ok = not problems
        all_ok = all_ok and ok
        lines.append("%s %-22s %2d 文件  %s" % ("✓" if ok else "✕", t["key"],
                                                 len(build_files(s)) if not problems else -1,
                                                 "" if ok else " | ".join(problems)))
    msg = "模板自检（%d 套）：\n" % len(TEMPLATES) + "\n".join(lines) + \
          "\n\n%s  共 %d 套，钩子 %d 个。" % ("全部通过 ✓" if all_ok else "存在失败 ✕",
                                              len(TEMPLATES), len(HOOKS))
    if verbose:
        print(msg)
    return all_ok, msg


def build_all(out_dir):
    out = Path(out_dir)
    out.mkdir(parents=True, exist_ok=True)
    summary = []
    for t in TEMPLATES:
        s = apply_template(t["key"])
        d = out / t["key"]
        import shutil
        if d.exists():
            shutil.rmtree(d)
        root, n = write_dir(s, d)
        summary.append({"key": t["key"], "name": t["name"], "dir": str(root), "files": n})
    (out / "manifest.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print("已导出 %d 套模板到 %s" % (len(summary), out))
    return out


def main():
    try:
        sys.stdout.reconfigure(encoding="utf-8")
    except Exception:
        pass
    args = sys.argv[1:]
    if "--help" in args or "-h" in args:
        print(__doc__)
        print("命令行：--selftest / --build-all DIR / --smoke / --gui-check / 无参数启动窗口")
        return 0
    if "--selftest" in args:
        ok, _ = run_selftest(verbose=True)
        return 0 if ok else 1
    if "--build-all" in args:
        i = args.index("--build-all")
        d = args[i + 1] if i + 1 < len(args) else (Path(tempfile.gettempdir()) / "nb_templates")
        build_all(str(d))
        return 0
    gui_check = "--gui-check" in args
    app = App(smoke="--smoke" in args or gui_check)
    if gui_check:
        app.after(300, app.run_gui_check)
    app.mainloop()
    return 0 if (not gui_check or getattr(app, "_gui_check_ok", False)) else 1


# 把页签实现挂到 App 上（前缀 app_ → 去掉前缀）
for _name, _fn in list(globals().items()):
    if _name.startswith("app_") and callable(_fn):
        setattr(App, _name[4:], _fn)

if __name__ == "__main__":
    sys.exit(main())



