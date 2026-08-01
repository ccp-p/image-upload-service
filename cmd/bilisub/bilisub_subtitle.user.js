// ==UserScript==
// @name          B站字幕批量获取
// @namespace     https://github.com/bilisub
// @version       1.0
// @description    批量获取B站多P视频字幕，支持VTT/SRT格式，可配合本地Go服务器自动保存到磁盘
// @author         bilisub
// @match         *://www.bilibili.com/video/*
// @match         *://www.bilibili.com/festival/*
// @grant         none
// @run-at         document-idle
// ==/UserScript==

(function () {
    'use strict';

   var SERVER_URL = 'http://localhost:9876';
   var DELAY_MS = 800;
   var PANEL_ID = 'bs-subtitle-panel';

    var DEFAULT_PROMPT = 'You are an expert video analyst., perform the following steps: \n\n1. Access and accurately transcribe the full video content, including key timestamps for reference.\n2. Deeply analyze the video to identify the core message, main concepts, supporting arguments, and any data or examples presented.\n3. Extract the essential knowledge points and organize them into a concise, structured summary (aim for 300-600 words unless specified otherwise).\n4. For each major point, explain it using 1-2 clear analogies to make complex ideas more relatable and easier to understand (e.g., compare abstract concepts to everyday scenarios).\n5. Provide a critical analysis section: Discuss pros and cons, different perspectives (e.g., educational, ethical, practical), public opinions based on general trends, and any science/data-backed facts if applicable.\n6. If relevant, include a customizable step-by-step actionable framework derived from the content.\n7. End with memory aids like mnemonics or anchors for better retention, plus a final verdict or calculation (e.g., efficiency score or key takeaway metric).\n\nOutput everything in a well-formatted response with Markdown headers for sections. Ensure the summary is objective, accurate, and spoiler-free if it\'s entertainment content.';

    function sanitizeFilename(name) {
        return (name || '').replace(/[\/\\:*?"<>|]+/g, '_').trim();
    }

    function formatTime(sec) {
        if (sec < 0 || isNaN(sec)) sec = 0;
        var total = Math.floor(sec);
        var h = Math.floor(total / 3600);
        var m = Math.floor((total % 3600) / 60);
        var s = total % 60;
        var ms = Math.round((sec - total) * 1000);
        if (ms >= 1000) { ms = 0; s++; }
        if (s >= 60) { s = 0; m++; }
        if (m >= 60) { m = 0; h++; }
        function pad(n, l) { return String(n).padStart(l, '0'); }
        return pad(h, 2) + ':' + pad(m, 2) + ':' + pad(s, 2) + '.' + pad(ms, 3);
    }

    function toVTT(body) {
        var vtt = 'WEBVTT\n\n';
        for (var i = 0; i < body.length; i++) {
            vtt += formatTime(body[i].from) + ' --> ' + formatTime(body[i].to) + '\n';
            vtt += (body[i].content || '').trim() + '\n\n';
        }
        return vtt;
    }

    function toSRT(body) {
        var srt = '';
        for (var i = 0; i < body.length; i++) {
            srt += (i + 1) + '\n';
            srt += formatTime(body[i].from) + ' --> ' + formatTime(body[i].to) + '\n';
            srt += (body[i].content || '').trim() + '\n\n';
        }
        return srt;
    }

    // Strip timestamps, keep only subtitle text with dedup
    function toPlainText(body) {
        var lines = [];
        for (var i = 0; i < body.length; i++) {
            var text = (body[i].content || '').trim();
            if (!text) continue;
            if (lines.length === 0 || lines[lines.length - 1] !== text) {
                lines.push(text);
            }
        }
        return lines.join('\n');
    }

    function fixURL(url) {
        if (!url) return '';
        if (url.indexOf('//') === 0) return 'https:' + url;
        if (url.indexOf('http') !== 0) return 'https://' + url;
        return url;
    }

    function sleep(ms) {
        return new Promise(function (r) { setTimeout(r, ms); });
    }

    function fetchJSON(url, withCreds) {
        var opts = {};
        if (withCreds) opts.credentials = 'include';
        return fetch(url, opts).then(function (resp) {
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            return resp.json();
        });
    }

    function getVideoInfo() {
        var state = window.__INITIAL_STATE__;
        if (!state) return null;
        console.log('[B站字幕] state keys:', Object.keys(state));

        // Regular video: /video/BVxxx
        if (state.videoData) {
            var vd = state.videoData;
            console.log('[B站字幕] videoData:', vd.aid, vd.bvid, vd.title, (vd.pages || []).length, 'pages');
            var pages = vd.pages || [];
            // If no pages, try to build from cid + page info
            if (pages.length === 0 && vd.cid) {
                pages = [{ page: 1, cid: vd.cid, part: vd.title || '' }];
            }
            if (pages.length > 0) {
                return {
                    type: 'video',
                    aid: vd.aid || 0,
                    bvid: vd.bvid || '',
                    title: vd.title || 'bilibili',
                    pages: pages.map(function (p) {
                        return { page: p.page || 1, cid: p.cid || 0, part: p.part || '' };
                    })
                };
            }
        }

        // Festival: /festival/xxx
        if (state.videoInfo) {
            return {
                type: 'festival',
                aid: state.videoInfo.aid || 0,
                bvid: state.videoInfo.bvid || '',
                title: state.videoInfo.title || 'festival',
                pages: [{ page: 1, cid: state.videoInfo.cid || 0, part: state.videoInfo.title || '' }]
            };
        }

        // Fallback: try to extract from URL
        var bvidMatch = location.pathname.match(/BV[0-9A-Za-z]{10}/);
        if (bvidMatch) {
            console.log('[B站字幕] fallback: using BV from URL');
            // We need aid/cid - try fetching from API
            return {
                type: 'video',
                aid: 0,
                bvid: bvidMatch[0],
                title: '加载中...',
                pages: [{ page: 1, cid: 0, part: '' }]
            };
        }

        return null;
    }

    function getSubtitleList(aid, cid) {
        var url = 'https://api.bilibili.com/x/player/wbi/v2?aid=' + aid + '&cid=' + cid;
        return fetchJSON(url, true).then(function (data) {
            if (data.code !== 0) throw new Error('API error ' + data.code);
            var subs = (data.data && data.data.subtitle && data.data.subtitle.subtitles) || [];
            return subs;
        });
    }

    function getSubtitleContent(subtitleUrl) {
        var url = fixURL(subtitleUrl);
        return fetchJSON(url, false).then(function (data) {
            return data.body || [];
        });
    }

    // Fallback: fetch video info from API using BV id from URL
    function fetchVideoInfoByAPI(bvid) {
        var url = 'https://api.bilibili.com/x/web-interface/view?bvid=' + bvid;
        return fetchJSON(url, true).then(function (data) {
            if (data.code !== 0) throw new Error('view API error ' + data.code);
            var d = data.data;
            var pages = (d.pages || []).map(function (p) {
                return { page: p.page || 1, cid: p.cid || 0, part: p.part || '' };
            });
            return {
                type: 'video',
                aid: d.aid || 0,
                bvid: d.bvid || bvid,
                title: d.title || bvid,
                pages: pages
            };
        });
    }

    function tryClickCC() {
        var selectors = [
            '.bpx-player-ctrl-subtitle',
            '.squirtle-subtitle',
            '.bpx-player-subtitle-setting-btn'
        ];
        for (var i = 0; i < selectors.length; i++) {
            var btn = document.querySelector(selectors[i]);
            if (btn) { btn.click(); return true; }
        }
        return false;
    }

    function checkServer() {
        return fetch(SERVER_URL + '/health').then(function (r) { return r.ok; }).catch(function () { return false; });
    }

    function sendToServer(batch) {
        return fetch(SERVER_URL + '/api/subtitles', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(batch)
        }).then(function (r) { return r.json(); });
    }

    function loadJSZip() {
        if (window.JSZip) return Promise.resolve(true);
        return new Promise(function (resolve) {
            var s = document.createElement('script');
            s.src = 'https://cdn.jsdelivr.net/npm/jszip@3.10.1/dist/jszip.min.js';
            s.onload = function () { resolve(true); };
            s.onerror = function () { resolve(false); };
            document.head.appendChild(s);
        });
    }

    function downloadBlob(content, filename) {
        var blob = (content instanceof Blob) ? content : new Blob([content], { type: 'text/plain;charset=utf-8' });
        var url = URL.createObjectURL(blob);
        var a = document.createElement('a');
        a.href = url;
        a.download = filename;
        document.body.appendChild(a);
        a.click();
        a.remove();
        setTimeout(function () { URL.revokeObjectURL(url); }, 1000);
    }

    function downloadZip(subtitles, baseName) {
        return loadJSZip().then(function (ok) {
            if (!ok) {
                return subtitles.reduce(function (p, sub) {
                    return p.then(function () { downloadBlob(sub.content, sub.filename); return sleep(300); });
                }, Promise.resolve());
            }
            var zip = new JSZip();
            for (var i = 0; i < subtitles.length; i++) {
                zip.file(subtitles[i].filename, subtitles[i].content);
            }
            return zip.generateAsync({ type: 'blob' }).then(function (blob) {
                downloadBlob(blob, baseName + '_subtitles.zip');
            });
        });
    }

    function createPanel(videoInfo, serverAvailable) {
        if (document.getElementById(PANEL_ID)) return;
        var pageCount = videoInfo.pages.length;

        // Use a host element with Shadow DOM to isolate from bilibili CSS
        var host = document.createElement('div');
        host.id = PANEL_ID;
        host.style.cssText = 'position:fixed!important;bottom:24px!important;right:24px!important;z-index:2147483647!important;margin:0!important;padding:0!important;display:block!important;width:340px!important;height:auto!important;top:auto!important;left:auto!important';
        var shadow = host.attachShadow ? host.attachShadow({ mode: 'open' }) : null;

        var style = document.createElement('style');
        style.textContent = [
            ':host{all:initial}',
            '*{box-sizing:border-box}',
            '.panel{position:fixed;bottom:24px;right:24px;width:340px;background:#1e1e2e;border:1px solid #313244;border-radius:10px;box-shadow:0 8px 32px rgba(0,0,0,.4);font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;font-size:13px;color:#cdd6f4;overflow:hidden}',
            '.header{background:#181825;padding:10px 14px;display:flex;justify-content:space-between;align-items:center;font-weight:600;color:#89b4fa;font-size:14px}',
            '.close-btn{background:none;border:none;color:#6c7086;cursor:pointer;font-size:20px;line-height:1;padding:0;margin:0}',
            '.close-btn:hover{color:#f38ba8}',
            '.body{padding:12px 14px}',
            '.info{color:#a6adc8;font-size:12px;margin-bottom:3px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}',
            '.row{margin:8px 0;display:flex;align-items:center;gap:6px;flex-wrap:wrap}',
            'select{background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:4px 8px;font-size:12px;outline:none}',
            'label{font-size:12px;color:#a6adc8;cursor:pointer;display:flex;align-items:center;gap:4px}',
            '.btn{width:100%;padding:8px;border:none;border-radius:6px;cursor:pointer;font-weight:600;font-size:13px;transition:opacity .2s}',
            '.btn-primary{background:#89b4fa;color:#1e1e2e}',
            '.btn:disabled{opacity:.5;cursor:not-allowed}',
            '.btn-secondary{background:#313244;color:#cdd6f4;margin-top:6px;border:1px solid #45475a}',
           '.p-item{display:flex;align-items:center;gap:4px;padding:3px 6px;font-size:11px;color:#a6adc8;cursor:pointer;border-radius:3px}.p-item:hover{background:#313244}.p-item input{margin:0;cursor:pointer;flex-shrink:0}.p-item label{cursor:pointer;flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px}',
            '.p-list{display:flex;flex-wrap:wrap;gap:2px}',
            '.progress{margin-top:10px;max-height:220px;overflow-y:auto;font-size:11px;font-family:"Cascadia Code",Consolas,monospace;line-height:1.6}',
            '.log-ok{color:#a6e3a1}',
            '.log-fail{color:#f38ba8}',
            '.log-info{color:#89b4fa}',
            '.log-warn{color:#f9e2af}',
            '.log-time{color:#6c7086}',
            '.server-ok{color:#a6e3a1;font-size:11px}',
            '.server-off{color:#6c7086;font-size:11px}'
        ].join('\n');

        var panelHTML = document.createElement('div');
        panelHTML.className = 'panel';
        panelHTML.innerHTML =
            '<div class="header"><span>\u5b57\u5e55\u6279\u91cf\u83b7\u53d6</span><button class="close-btn" title="\u5173\u95ed">&times;</button></div>' +
            '<div class="body">' +
            '<div class="info" title="' + videoInfo.title + '">\u89c6\u9891: ' + videoInfo.title + '</div>' +
            '<div class="info">\u5206P: ' + pageCount + ' \u4e2a (aid=' + videoInfo.aid + ')</div>' +
           '<div class="row"><label>\u683c\u5f0f</label>' +
            '<select id="bs-format"><option value="txt" selected>TXT</option><option value="vtt">VTT</option><option value="srt">SRT</option></select>' +
            '<label style="margin-left:8px"><input type="checkbox" id="bs-use-server">\u4fdd\u5b58\u5230\u670d\u52a1\u5668</label>' +
            '</div>' +
            '<div class="row" id="bs-server-status">' +
            (serverAvailable ? '<span class="server-ok">\u25cf \u670d\u52a1\u5668\u5df2\u8fde\u63a5</span>' : '<span class="server-off">\u25cb \u670d\u52a1\u5668\u672a\u8fd0\u884c\uff0c\u5c06\u4e0b\u8f7dzip</span>') +
            '</div>' +
            '<details id="bs-ai-config" style="margin:6px 0">' +
            '<summary style="cursor:pointer;font-size:11px;color:#89b4fa;user-select:none">AI Config</summary>' +
            '<div style="margin-top:4px">' +
            '<div class="row" style="align-items:center;gap:4px">' +
            '<label style="font-size:11px;min-width:48px">API URL</label>' +
            '<input type="text" id="bs-api-url" placeholder="https://your-api.com/" style="flex:1;background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:3px 6px;font-size:11px;outline:none;min-width:0">' +
            '</div>' +
            '<div class="row" style="align-items:center;gap:4px">' +
            '<label style="font-size:11px;min-width:48px">API Key</label>' +
            '<input type="password" id="bs-api-key" placeholder="sk-..." style="flex:1;background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:3px 6px;font-size:11px;outline:none;min-width:0">' +
            '</div>' +
            '<div class="row" style="align-items:center;gap:4px">' +
            '<label style="font-size:11px;min-width:48px">Model</label>' +
           '<input type="text" id="bs-model" placeholder="glm-5.2" style="flex:1;background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:3px 6px;font-size:11px;outline:none;min-width:0">' +
           '</div>' +
            '<div class="row" style="flex-direction:column;align-items:stretch;gap:2px">' +
            '<label style="font-size:11px">Prompt</label>' +
            '<textarea id="bs-prompt" rows="6" placeholder="System prompt for AI..." style="background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:4px 6px;font-size:11px;outline:none;resize:vertical;font-family:inherit;min-width:0"></textarea>' +
            '</div>' +
           '</div>' +
            '</details>' +
            '<div class="p-list" id="bs-p-list" style="max-height:180px;overflow-y:auto;background:#181825;border:1px solid #313244;border-radius:4px;padding:4px;margin:6px 0"></div>' +
            '<div class="row"><button class="btn-sm" id="bs-select-all" style="background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:3px 10px;font-size:11px;cursor:pointer">\u5168\u9009</button>' +
            '<button class="btn-sm" id="bs-select-none" style="background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;padding:3px 10px;font-size:11px;cursor:pointer;margin-left:4px">\u53d6\u6d88\u5168\u9009</button></div>' +
           '<button class="btn btn-primary" id="bs-start">\u83b7\u53d6\u9009\u4e2dP\u5b57\u5e55</button>' +
            '<div class="row" style="gap:10px;margin-top:6px">' +
            '<label style="font-size:11px"><input type="checkbox" id="bs-auto-fetch" checked>\u81ea\u52a8\u83b7\u53d6</label>' +
            '<label style="font-size:11px"><input type="checkbox" id="bs-auto-summarize">\u81ea\u52a8\u603b\u7ed3</label>' +
            '</div>' +
           '<button class="btn btn-secondary" id="bs-copy" style="display:none">\u590d\u5236\u7eaf\u6587\u672c</button>' +
            '<button class="btn btn-secondary" id="bs-summarize" disabled style="margin-top:6px;background:#313244;border:1px solid #cba6f7;color:#cba6f7;opacity:.5;cursor:not-allowed">\u2709 AI\u603b\u7ed3</button>' +
            '<div class="progress" id="bs-log"></div>' +
            '</div>';

        if (shadow) {
            shadow.appendChild(style);
            shadow.appendChild(panelHTML);
        } else {
            // Fallback: no shadow DOM
            host.appendChild(style);
            host.appendChild(panelHTML);
        }

        document.body.appendChild(host);

        var root = shadow || host;
        root.querySelector('.close-btn').onclick = function () { host.remove(); };

        // Load saved AI config
        var apiUrl = root.querySelector('#bs-api-url');
        var apiKey = root.querySelector('#bs-api-key');
       var model = root.querySelector('#bs-model');
       if (localStorage.getItem('bs-api-url')) apiUrl.value = localStorage.getItem('bs-api-url');
       if (localStorage.getItem('bs-api-key')) apiKey.value = localStorage.getItem('bs-api-key');
       if (localStorage.getItem('bs-model')) model.value = localStorage.getItem('bs-model');
       apiUrl.addEventListener('change', function () { localStorage.setItem('bs-api-url', apiUrl.value); });
       apiKey.addEventListener('change', function () { localStorage.setItem('bs-api-key', apiKey.value); });
       model.addEventListener('change', function () { localStorage.setItem('bs-model', model.value); });
        var prompt = root.querySelector('#bs-prompt');
        if (localStorage.getItem('bs-prompt')) prompt.value = localStorage.getItem('bs-prompt');
        else prompt.value = DEFAULT_PROMPT;
       prompt.addEventListener('change', function () { localStorage.setItem('bs-prompt', prompt.value); });

        var autoFetch = root.querySelector('#bs-auto-fetch');
        var autoSummarize = root.querySelector('#bs-auto-summarize');
        if (localStorage.getItem('bs-auto-fetch') === 'false') autoFetch.checked = false;
        if (localStorage.getItem('bs-auto-summarize') === 'true') autoSummarize.checked = true;
        autoFetch.addEventListener('change', function () { localStorage.setItem('bs-auto-fetch', autoFetch.checked); });
        autoSummarize.addEventListener('change', function () { localStorage.setItem('bs-auto-summarize', autoSummarize.checked); });

       // Wire summarize button
        var summarizeBtn = root.querySelector('#bs-summarize');
        if (summarizeBtn) summarizeBtn.onclick = summarizeSubtitles;
        root.querySelector('#bs-start').onclick = startDownload;
        root.querySelector('#bs-copy').onclick = copyAllText;

        if (!serverAvailable) {
            var cb = root.querySelector('#bs-use-server');
            if (cb) { cb.disabled = true; if(cb.parentElement)cb.parentElement.style.color = '#6c7086'; }
        }

        // Store root reference for updateServerStatus
        host._shadowRoot = root;

        // Populate P list
        var pList = root.querySelector('#bs-p-list');
        if (pList) {
            videoInfo.pages.forEach(function (pg) {
               var item = document.createElement('div');
                item.className = 'p-item';
               item.innerHTML = '<input type="checkbox" value="' + pg.page + '">' +
                    '<label title="P' + pg.page + ': ' + pg.part + '">P' + pg.page + ' ' + pg.part + '</label>';
                pList.appendChild(item);
            });
        }

        var selectAllBtn = root.querySelector('#bs-select-all');
        if (selectAllBtn) selectAllBtn.onclick = function () {
            root.querySelectorAll('#bs-p-list input').forEach(function (cb) { });
        };
        var selectNoneBtn = root.querySelector('#bs-select-none');
        if (selectNoneBtn) selectNoneBtn.onclick = function () {
            root.querySelectorAll('#bs-p-list input').forEach(function (cb) { cb.checked = false; });
        };
    }

    function getRoot() {
        var panel = document.getElementById(PANEL_ID);
        if (!panel) return null;
        return panel._shadowRoot || panel;
    }

    function updateServerStatus(available) {
        var root = getRoot();
        if (!root) return;
        var statusEl = root.querySelector('#bs-server-status');
        var cb = root.querySelector('#bs-use-server');
        if (available) {
            if (cb) { cb.disabled = false; if(cb.parentElement)cb.parentElement.style.color = '#a6adc8'; }
        } else {
            if (statusEl) statusEl.innerHTML = '<span class="server-off">○ 服务器未运行，将下载zip</span>';
            if (cb) { cb.disabled = true; cb.checked = false; if(cb.parentElement)cb.parentElement.style.color = '#6c7086'; }
        }
    }

   var allSubtitles = [];
   var allText = '';
    var subtitleCache = {};
    var lastFetchedP = null;
    var isFetching = false;

    function log(msg, type) {
        var root = getRoot();
        if (!root) { console.log('[B站字幕]', msg); return; }
        var logEl = root.querySelector('#bs-log');
        if (!logEl) { console.log('[B站字幕]', msg); return; }
        var time = new Date().toTimeString().split(' ')[0];
        var line = document.createElement('div');
        line.className = 'log-' + (type || 'info');
        line.innerHTML = '<span class="log-time">[' + time + ']</span> ' + msg;
        logEl.appendChild(line);
        logEl.scrollTop = logEl.scrollHeight;
    }

    function setButton(text, disabled) {
        var root = getRoot();
        if (!root) return;
        var btn = root.querySelector('#bs-start');
        if (btn) { btn.textContent = text; btn.disabled = disabled; }
    }

    function startDownload() {
        var videoInfo = getVideoInfo();
        if (!videoInfo) { log('无法获取视频信息', 'fail'); return; }

        // If aid/cid is 0, try fetching from API
        if (!videoInfo.aid || (videoInfo.pages.length > 0 && !videoInfo.pages[0].cid)) {
            if (!videoInfo.bvid) { log('无法获取BV号', 'fail'); return; }
            log('从API获取视频信息...', 'info');
            setButton('获取视频信息...', true);
            fetchVideoInfoByAPI(videoInfo.bvid).then(function (info) {
                videoInfo = info;
                log('视频: ' + info.title + ' (' + info.pages.length + 'P)', 'info');
                return doStartDownload(videoInfo);
            }).catch(function (err) {
                log('获取视频信息失败: ' + err.message, 'fail');
                setButton('获取选中P字幕', false);
            });
            return;
        }

        return doStartDownload(videoInfo);
    }

    function doStartDownload(videoInfo) {

        var root = getRoot();
        if (!root) { console.log('[B站字幕] panel not found'); return; }
        var formatEl = root.querySelector('#bs-format');
        var format = formatEl ? formatEl.value : 'txt';
        var serverCb = root.querySelector('#bs-use-server');
        var useServer = serverCb ? serverCb.checked : false;
        var ext = format === 'srt' ? 'srt' : (format === 'txt' ? 'txt' : 'vtt');
        var baseName = sanitizeFilename(videoInfo.title);

       setButton('获取中...', true);
       allSubtitles = [];
       allText = '';
        isFetching = true;

        var success = 0, failed = 0;
        var chain = Promise.resolve();

        var selectedPages = [];
        var checkboxes = root.querySelectorAll('#bs-p-list input:checked');
        var selSet = {};
        checkboxes.forEach(function (cb) { selSet[cb.value] = true; });
        var pagesToProcess = videoInfo.pages.filter(function (pg) { return selSet[pg.page]; });
        if (pagesToProcess.length === 0) { log('请至少选择一个P', 'fail'); setButton('获取选中P字幕', false); return; }

        pagesToProcess.forEach(function (pg, idx) {
            chain = chain.then(function () {
                var prefix = videoInfo.pages.length > 1 ? 'P' + pg.page + ' ' : '';
                log(prefix + '获取字幕列表 (cid=' + pg.cid + ')...', 'info');

                return getSubtitleList(videoInfo.aid, pg.cid).then(function (subs) {
                    if (subs.length === 0) {
                        var currentP = new URLSearchParams(location.search).get('p') || '1';
                        if (String(pg.page) === currentP) {
                            log(prefix + '未发现字幕，尝试开启CC...', 'warn');
                            tryClickCC();
                            return sleep(2000).then(function () {
                                return getSubtitleList(videoInfo.aid, pg.cid);
                            });
                        }
                    }
                    return subs;
                }).then(function (subs) {
                    if (subs.length === 0) {
                        log(prefix + '✗ 无字幕', 'fail');
                        failed++;
                        return;
                    }

                    var chosen = 0;
                    for (var j = 0; j < subs.length; j++) {
                        if (subs[j].lan && subs[j].lan.indexOf('zh') === 0) { chosen = j; break; }
                    }

                    log(prefix + '下载: ' + (subs[chosen].lan_doc || subs[chosen].lan), 'info');

                    return getSubtitleContent(subs[chosen].subtitle_url).then(function (body) {
                        if (!body || body.length === 0) {
                            log(prefix + '✗ 字幕内容为空', 'fail');
                            failed++;
                            return;
                        }

                        var content = format === 'srt' ? toSRT(body) : (format === 'txt' ? toPlainText(body) : toVTT(body));
                        var filename;
                        if (videoInfo.pages.length > 1) {
                            filename = sanitizeFilename(pg.part) + '.' + ext;
                        } else {
                            filename = (sanitizeFilename(pg.part) || baseName) + '.' + ext;
                        }

                        allSubtitles.push({
                            filename: filename,
                            content: content,
                            page: pg.page,
                            part: pg.part,
                            language: subs[chosen].lan_doc || subs[chosen].lan
                        });

                        for (var k = 0; k < body.length; k++) {
                            var text = (body[k].content || '').trim();
                            if (text) allText += text + '\n';
                        }

                        log(prefix + '✓ ' + filename, 'ok');
                        success++;
                    });
                }).catch(function (err) {
                    log(prefix + '✗ ' + err.message, 'fail');
                    failed++;
                }).then(function () {
                    if (idx < pagesToProcess.length - 1) return sleep(DELAY_MS);
                });
            });
        });

        return chain.then(function () {
            log('完成: ' + success + ' 成功, ' + failed + ' 失败', 'info');

            var copyBtn = root.querySelector('#bs-copy');
            var summarizeBtn2 = root.querySelector('#bs-summarize');
            if (copyBtn && allSubtitles.length > 0) copyBtn.style.display = 'block';
            if (summarizeBtn2 && allSubtitles.length > 0) { summarizeBtn2.disabled = false; summarizeBtn2.style.opacity = '1'; summarizeBtn2.style.cursor = 'pointer'; }
           if (allSubtitles.length === 0) {
               setButton('获取选中P字幕', false);
               return;
           }

            if (useServer) {
                log('发送到服务器...', 'info');
                return sendToServer({
                    title: videoInfo.title,
                    bvid: videoInfo.bvid,
                    format: format,
                    subtitles: allSubtitles
                }).then(function (result) {
                    if (result.success) {
                        log('服务器已保存 ' + result.saved_count + ' 个文件', 'ok');
                    } else {
                        log('服务器保存失败: ' + (result.error || 'unknown'), 'fail');
                    }
                }).catch(function (err) {
                    log('服务器连接失败: ' + err.message, 'fail');
                });
            }
           log('已获取 ' + allSubtitles.length + ' 个字幕', 'ok');
       }).then(function () {
           setButton('获取选中P字幕', false);
            isFetching = false;
            lastFetchedP = getCurrentP();
            // Auto-summarize if enabled
            if (allSubtitles.length > 0) {
                var autoSumCb = root.querySelector('#bs-auto-summarize');
                if (autoSumCb && autoSumCb.checked) {
                    log('自动触发AI总结...', 'info');
                    summarizeSubtitles();
                }
            }
       });
   }

    function copyAllText() {
        if (!allText) return;
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(allText).then(function () {
                log('纯文本已复制到剪贴板', 'ok');
            }).catch(function () { fallbackCopy(allText); });
        } else {
            fallbackCopy(allText);
        }
    }

    function fallbackCopy(text) {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.style.cssText = 'position:fixed;opacity:0';
        document.body.appendChild(ta);
        ta.select();
        try { document.execCommand('copy'); log('纯文本已复制到剪贴板', 'ok'); }
        catch (e) { log('复制失败', 'fail'); }
        ta.remove();
    }

    function summarizeSubtitles() {
        var root = getRoot();
        if (!root) return;

        if (allSubtitles.length === 0) {
            log('\u8bf7\u5148\u83b7\u53d6\u5b57\u5e55', 'fail');
            return;
        }

        var apiUrl = root.querySelector('#bs-api-url').value.trim();
        var apiKey = root.querySelector('#bs-api-key').value.trim();
        var model = root.querySelector('#bs-model').value.trim() || 'glm-5.2';
        var prompt = root.querySelector('#bs-prompt').value.trim() || DEFAULT_PROMPT;

        if (!apiUrl || !apiKey) {
            log('\u8bf7\u5148\u5728AI\u914d\u7f6e\u4e2d\u586b\u5199 API URL \u548c API Key', 'fail');
            return;
        }

        var summarizeBtn = root.querySelector('#bs-summarize');
        if (summarizeBtn) { summarizeBtn.disabled = true; summarizeBtn.textContent = 'AI \u603b\u7ed3\u4e2d...'; }

        var combinedText = "";
        allSubtitles.forEach(function (sub) {
            combinedText += '=== P' + sub.page + ' ' + sub.part + '===\n';
            combinedText += sub.content + '\n\n';
        });

        log('\u53d1\u9001\u5230AI (' + model + ')...', 'info');

        fetch(SERVER_URL + "/api/summarize", {
            method: 'POST',
           headers: { 'Content-Type': 'application/json' },
           body: JSON.stringify({ text: combinedText, api_url: apiUrl, api_key: apiKey, model: model, system_prompt: prompt })
        }).then(function (resp) { return resp.json(); }).then(function (data) {
            if (!data.success) {
                throw new Error(data.error || 'unknown');
            }
            var summary = data.summary || '';
            if (summary) {
                log('AI\u603b\u7ed3\u5df2\u751f\u6210:', 'ok');
                var logEl = root.querySelector('#bs-log');
                if (logEl) {
                    var summaryDiv = document.createElement("div");
                    summaryDiv.style.cssText = 'background:#181825;border:1px solid #cba6f7;border-radius:6px;padding:10px;margin-top:8px;color:#cdd6f4;font-size:12px;line-height:1.6;white-space:pre-wrap;max-height:400px;overflow-y:auto';
                    summaryDiv.textContent = summary;
                    logEl.appendChild(summaryDiv);
                    logEl.scrollTop = logEl.scrollHeight;
                }
                if (navigator.clipboard && navigator.clipboard.writeText) {
                    navigator.clipboard.writeText(summary).then(function () {
                        log('\u603b\u7ed3\u5df2\u590d\u5236\u5230\u526a\u8d34\u677f', 'ok');
                    });
                }
            } else {
                log('AI\u8fd4\u56de\u7a7a\u5185\u5bb9', 'fail');
            }
        }).catch(function (err) {
            log('AI\u8bf7\u6c42\u5931\u8d25: ' + err.message, 'fail');
        }).then(function () {
            if (summarizeBtn) { summarizeBtn.disabled = false; summarizeBtn.textContent = '\u2709 AI\u603b\u7ed3'; }
        });
    }

    function getCurrentP() {
        var p = new URLSearchParams(location.search).get('p');
        return p ? parseInt(p, 10) : 1;
    }

   function syncCurrentP() {
       var root = getRoot();
       if (!root) return;
       var currentP = getCurrentP();
       var allCbs = root.querySelectorAll('#bs-p-list input');
       var found = false;
       allCbs.forEach(function (cb) {
           if (parseInt(cb.value, 10) === currentP) {
               cb.checked = true;
               found = true;
           } else {
               cb.checked = false;
           }
       });
       if (found) {
           console.log('[B站字幕] synced to P' + currentP);
            var pItem = allCbs.forEach(function (cb) {
                if (parseInt(cb.value, 10) === currentP && cb.parentElement) {
                    cb.parentElement.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
                }
            });
       }
    }

   var lastUrl = '';
    var currentBVID = '';

    function getBVIDFromUrl() {
        var m = location.pathname.match(/BV[0-9A-Za-z]{10}/);
        return m ? m[0] : '';
    }

    function watchUrlChange() {
        if (location.href !== lastUrl) {
            lastUrl = location.href;
            var newBV = getBVIDFromUrl();
            if (newBV && newBV !== currentBVID) {
                console.log('[B站字幕] BV changed: ' + currentBVID + ' -> ' + newBV);
                currentBVID = newBV;
                rebuildPanel();
            } else {
                syncCurrentP();
                autoFetchCurrentP();
            }
        }
    }

    function rebuildPanel() {
        // Remove old panel
        var old = document.getElementById(PANEL_ID);
        if (old) old.remove();
        // Reset state
        allSubtitles = [];
        allText = '';
        lastFetchedP = null;
        isFetching = false;
        // Wait for __INITIAL_STATE__ to update for new video
        var retries = 30;
        (function wait() {
            var vi = getVideoInfo();
            if (vi && vi.bvid === currentBVID) {
                createPanel(vi, false);
                syncCurrentP();
                autoFetchCurrentP();
            } else if (retries > 0) {
                retries--;
                setTimeout(wait, 500);
            } else {
                console.log('[B站字幕] rebuildPanel: video info not ready');
            }
       })();
   }

   function autoFetchCurrentP() {
        var root = getRoot();
        if (!root) return;
        var autoFetchCb = root.querySelector('#bs-auto-fetch');
        if (!autoFetchCb || !autoFetchCb.checked) return;
        if (isFetching) return;
        var currentP = getCurrentP();
        if (lastFetchedP === currentP) return;
        // Delay to let Bilibili SPA load the new page content
        setTimeout(function () {
            syncCurrentP();
            var startBtn = root.querySelector('#bs-start');
            if (startBtn && !startBtn.disabled) {
                console.log('[B站字幕] auto-fetching P' + getCurrentP());
                startDownload();
            }
       }, 2000);
   }

   function init() {
        console.log('[B站字幕] init start');
        var retries = 30;
        (function wait() {
            if (window.__INITIAL_STATE__) {
                console.log('[B站字幕] __INITIAL_STATE__ found');
                var videoInfo = getVideoInfo();
                console.log('[B站字幕] videoInfo:', videoInfo);
                if (!videoInfo) {
                    console.log('[B站字幕] videoInfo null, __INITIAL_STATE__ keys:', Object.keys(window.__INITIAL_STATE__));
                    videoInfo = { title: 'unknown', aid: 0, bvid: '', pages: [{page:1,cid:0,part:''}] };
                }
               // Create panel immediately
               createPanel(videoInfo, false);
                currentBVID = getBVIDFromUrl();
               // Sync current P on load
               lastUrl = location.href;
               syncCurrentP();
                autoFetchCurrentP();
               // Poll server status every 5 seconds
                function pollServer() {
                    checkServer().then(function (ok) {
                        updateServerStatus(ok);
                    }).catch(function () {
                        updateServerStatus(false);
                    });
                }
                pollServer();
                setInterval(pollServer, 5000);
                // Watch for P changes (Bilibili is SPA, URL changes without page reload)
                setInterval(watchUrlChange, 1000);
            } else if (retries > 0) {
                retries--;
                setTimeout(wait, 500);
            } else {
                console.log('[B站字幕] __INITIAL_STATE__ not found after retries');
            }
        })();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', function () { setTimeout(init, 1500); });
    } else {
        setTimeout(init, 1500);
    }
})();
