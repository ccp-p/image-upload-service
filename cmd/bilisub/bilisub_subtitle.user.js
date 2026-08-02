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

    var THEME_CSS = ":root{--bg:#0a0e17;--surface:#111827;--surface-2:#1a2332;--accent:#f0c040;--accent-dim:#c49a20;--blue:#4da6ff;--green:#34d399;--red:#f87171;--purple:#a78bfa;--orange:#fb923c;--cyan:#22d3ee;--text:#e8e6e1;--text-muted:#8896a7;--text-dim:#556378;--border:#2a3a4e;--radius:12px}*{margin:0;padding:0;box-sizing:border-box}body{background:var(--bg);color:var(--text);font-family:\"Noto Sans SC\",sans-serif;font-weight:300;line-height:1.8;overflow-x:hidden}body::before{content:\"\";position:fixed;inset:0;background:radial-gradient(ellipse at 15% 10%,rgba(77,166,255,.06) 0%,transparent 50%),radial-gradient(ellipse at 85% 80%,rgba(240,192,64,.05) 0%,transparent 50%),radial-gradient(ellipse at 50% 50%,rgba(167,139,250,.03) 0%,transparent 60%);pointer-events:none;z-index:0}.container{position:relative;z-index:2;max-width:1100px;margin:0 auto;padding:0 24px}h1{font-family:\"Noto Serif SC\",serif;font-weight:900;font-size:clamp(28px,5vw,42px);line-height:1.2;margin:20px 0 12px}h2{font-family:\"Noto Serif SC\",serif;font-weight:700;font-size:clamp(22px,3.5vw,30px);margin:18px 0 10px}h3{font-family:\"Noto Serif SC\",serif;font-weight:700;font-size:18px;margin:14px 0 8px}h4{font-family:\"Noto Serif SC\",serif;font-weight:700;font-size:16px;margin:12px 0 6px}h5,h6{font-family:\"Noto Serif SC\",serif;font-weight:700;font-size:15px;margin:10px 0 6px;color:var(--text-muted)}p{margin:8px 0;color:var(--text-muted)}a{color:var(--blue);text-decoration:none}strong{color:var(--text)}em{color:var(--accent);font-style:normal}ul,ol{padding-left:24px;margin:8px 0}li{margin:4px 0;color:var(--text-muted)}blockquote{border-left:3px solid var(--accent);padding:12px 18px;margin:14px 0;background:var(--surface);border-radius:0 var(--radius) var(--radius) 0;color:var(--text-muted)}hr{border:none;border-top:1px solid var(--border);margin:24px 0}.hero{padding:50px 0 40px;border-bottom:1px solid var(--border)}.hero-badge{display:inline-flex;align-items:center;gap:8px;padding:6px 16px;border:1px solid var(--accent-dim);border-radius:20px;font-size:13px;color:var(--accent);font-family:\"JetBrains Mono\",monospace;letter-spacing:.08em;margin-bottom:20px}.hero h1{margin-bottom:14px}.hero h1 em{font-style:normal;color:var(--accent)}.hero p{font-size:16px;max-width:680px}section{padding:40px 0;border-bottom:1px solid var(--border)}.section-label{font-family:\"JetBrains Mono\",monospace;font-size:12px;color:var(--blue);letter-spacing:.15em;text-transform:uppercase;margin-bottom:8px}.section-title{font-family:\"Noto Serif SC\",serif;font-weight:700;font-size:clamp(22px,3.5vw,30px);margin-bottom:10px}.section-desc{font-size:15px;max-width:700px;margin-bottom:28px}.concept-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(260px,1fr));gap:16px}.concept-card{background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:22px;transition:border-color .3s,transform .3s}.concept-card:hover{border-color:var(--text-dim);transform:translateY(-2px)}.concept-icon{width:38px;height:38px;border-radius:10px;display:flex;align-items:center;justify-content:center;font-size:17px;margin-bottom:12px;font-family:\"JetBrains Mono\",monospace;font-weight:700}.concept-card[data-color=accent] .concept-icon{background:rgba(240,192,64,.12);color:var(--accent)}.concept-card[data-color=blue] .concept-icon{background:rgba(77,166,255,.12);color:var(--blue)}.concept-card[data-color=green] .concept-icon{background:rgba(52,211,153,.12);color:var(--green)}.concept-card[data-color=red] .concept-icon{background:rgba(248,113,113,.12);color:var(--red)}.concept-card[data-color=purple] .concept-icon{background:rgba(167,139,250,.12);color:var(--purple)}.concept-card[data-color=orange] .concept-icon{background:rgba(251,146,60,.12);color:var(--orange)}.concept-card h3{font-size:16px;margin-bottom:6px}.concept-card p{font-size:14px}.code-block{background:#0d1117;border:1px solid var(--border);border-radius:var(--radius);overflow:hidden;margin:16px 0}.code-header{display:flex;align-items:center;gap:7px;padding:10px 16px;background:var(--surface-2);border-bottom:1px solid var(--border)}.code-dot{width:10px;height:10px;border-radius:50%}.code-dot:nth-child(1){background:#ff5f57}.code-dot:nth-child(2){background:#febc2e}.code-dot:nth-child(3){background:#28c840}.code-title{font-family:\"JetBrains Mono\",monospace;font-size:12px;color:var(--text-dim);margin-left:6px}pre{padding:16px 20px;overflow-x:auto;font-family:\"JetBrains Mono\",monospace;font-size:13px;line-height:1.7;color:#c9d1d9}.flow-container{display:flex;flex-direction:column;align-items:center;gap:0;padding:20px 0}.flow-step{display:flex;align-items:center;gap:16px;width:100%;max-width:650px}.flow-number{width:42px;height:42px;min-width:42px;border-radius:50%;display:flex;align-items:center;justify-content:center;font-family:\"JetBrains Mono\",monospace;font-weight:700;font-size:16px;border:2px solid}.flow-step[data-color=accent] .flow-number{border-color:var(--accent);color:var(--accent)}.flow-step[data-color=blue] .flow-number{border-color:var(--blue);color:var(--blue)}.flow-step[data-color=green] .flow-number{border-color:var(--green);color:var(--green)}.flow-step[data-color=purple] .flow-number{border-color:var(--purple);color:var(--purple)}.flow-step[data-color=orange] .flow-number{border-color:var(--orange);color:var(--orange)}.flow-content{flex:1;background:var(--surface);border:1px solid var(--border);border-radius:var(--radius);padding:14px 20px}.flow-content h4{font-size:15px;margin-bottom:3px}.flow-content p{font-size:13px}.flow-line{width:2px;height:20px;background:var(--border);margin-left:20px}.warn-box{background:rgba(248,113,113,.06);border:1px solid rgba(248,113,113,.2);border-left:3px solid var(--red);border-radius:var(--radius);padding:16px 20px;margin:16px 0}.warn-box h4{color:var(--red);font-size:13px;font-weight:700;margin-bottom:5px;font-family:\"JetBrains Mono\",monospace}.tip-box{background:rgba(52,211,153,.06);border:1px solid rgba(52,211,153,.2);border-left:3px solid var(--green);border-radius:var(--radius);padding:16px 20px;margin:16px 0}.tip-box h4{color:var(--green);font-size:13px;font-weight:700;margin-bottom:5px;font-family:\"JetBrains Mono\",monospace}.cmp-table{width:100%;border-collapse:separate;border-spacing:0;border:1px solid var(--border);border-radius:var(--radius);overflow:hidden;margin:16px 0;font-size:14px}.cmp-table th{background:var(--surface-2);padding:12px 18px;text-align:left;font-weight:700;font-size:13px;color:var(--text-muted);font-family:\"JetBrains Mono\",monospace;border-bottom:1px solid var(--border)}.cmp-table td{padding:12px 18px;border-bottom:1px solid var(--border);color:var(--text-muted)}.cmp-table tr:last-child td{border-bottom:none}.summary-bar{display:flex;gap:2px;border-radius:var(--radius);overflow:hidden;margin:20px 0;height:5px}.summary-bar div{flex:1}.footer{padding:28px 0;text-align:center;color:var(--text-dim);font-size:13px}code{font-family:\"JetBrains Mono\",monospace;font-size:.88em;background:var(--surface-2);border:1px solid var(--border);padding:2px 7px;border-radius:4px;color:var(--accent)}@keyframes fadeUp{from{opacity:0;transform:translateY(20px)}to{opacity:1;transform:translateY(0)}}.fade-in{opacity:0;animation:fadeUp .6s ease forwards}.fade-in:nth-child(2){animation-delay:.08s}.fade-in:nth-child(3){animation-delay:.16s}.fade-in:nth-child(4){animation-delay:.24s}@media(max-width:768px){.concept-grid{grid-template-columns:1fr}.flow-step{max-width:100%}pre{font-size:12px;padding:14px}}";

    var DEFAULT_PROMPT = '你是一位专业的视频内容分析师兼前端设计师。请分析视频字幕，生成一份深色主题的可视化要点解析页面。\n\n## 设计系统\n深色主题：背景 #0a0e17，卡片背景 #111827，边框 #2a3a4e。强调色：金色 #f0c040，蓝 #4da6ff，绿 #34d399，红 #f87171，紫 #a78bfa，橙 #fb923c。标题用衬线体，标签/代码用等宽体，正文用无衬线体。CSS 由渲染器自动注入，你只需用以下 class 写 body 内容。\n\n## 可用组件\n1. Hero：.hero > .hero-badge + h1(关键词用<em>) + p\n2. 分区：<section> > .section-label + h2.section-title + p.section-desc\n3. 概念卡片：.concept-grid > .concept-card[data-color="blue|green|accent|red|purple|orange"] > .concept-icon + h3 + p\n4. 代码块：.code-block > .code-header(.code-dot×3+.code-title) + pre\n5. 流程图：.flow-container > .flow-step[data-color](.flow-number+.flow-content>h4+p) + .flow-line\n6. 警告框：.warn-box > h4 + p\n7. 提示框：.tip-box > h4 + p\n8. 对比表：.cmp-table > thead/tbody\n9. 色彩条：.summary-bar > div×N\n\n## 分析要求\n1. 准确理解视频内容，提取核心观点和关键知识点。\n2. 深度分析视频的主旨、概念、论据，以及提到的数据或案例。\n3. 为每个核心要点提供清晰的类比，将复杂概念与日常场景关联。\n4. 提供批判性分析：讨论优缺点、不同视角、公众观点。\n5. 如适用，提供可执行的步骤框架和记忆辅助。\n\n## 输出要求\n- 必须使用中文回复。\n- 只返回 HTML body 内容（不要 <!DOCTYPE>、<html>、<head>、<style>，CSS 由渲染器自动注入）。\n- 使用上述组件 class 名生成内容。\n- 用 <section> 划分内容板块，每个板块用不同组件类型呈现。\n- 善用不同 data-color 区分信息类型，内容要有层次感。\n- 不要使用 Markdown 语法，不要用代码块包裹。';

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

    function escapeHTML(s) {
        return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
    }

    // Lightweight Markdown -> HTML (dark-theme inline styles)
    function markdownToHTML(md) {
        var blocks = [];
        md = md.replace(/```(\w*)\n([\s\S]*?)```/g, function (m, lang, code) {
            blocks.push('<pre style="background:#0d1117;padding:16px 20px;border-radius:12px;overflow:auto;border:1px solid #2a3a4e;color:#c9d1d9"><code>' + escapeHTML(code.replace(/\n$/, '')) + '</code></pre>');
            return '\u0000B' + (blocks.length - 1) + '\u0000';
        });
        var inlines = [];
        md = md.replace(/`([^`]+)`/g, function (m, code) {
            inlines.push('<code style="background:#1a2332;border:1px solid #2a3a4e;padding:2px 7px;border-radius:4px;color:#f0c040;font-size:.88em">' + escapeHTML(code) + '</code>');
            return '\u0000I' + (inlines.length - 1) + '\u0000';
        });
        md = escapeHTML(md);
        md = md.replace(/^######\s+(.+)$/gm, '<h6>$1</h6>');
        md = md.replace(/^#####\s+(.+)$/gm, '<h5>$1</h5>');
        md = md.replace(/^####\s+(.+)$/gm, '<h4>$1</h4>');
        md = md.replace(/^###\s+(.+)$/gm, '<h3>$1</h3>');
        md = md.replace(/^##\s+(.+)$/gm, '<h2>$1</h2>');
        md = md.replace(/^#\s+(.+)$/gm, '<h1>$1</h1>');
        md = md.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
        md = md.replace(/(^|[^*])\*([^*]+)\*/g, '$1<em>$2</em>');
        md = md.replace(/!\[([^\]]*)\]\(([^)]+)\)/g, '<img src="$2" alt="$1" style="max-width:100%;border-radius:8px">');
        md = md.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" style="color:#4da6ff">$1</a>');
        md = md.replace(/^>\s+(.+)$/gm, '<blockquote style="border-left:3px solid #f0c040;padding:12px 18px;margin:14px 0;background:#111827;border-radius:0 12px 12px 0;color:#8896a7">$1</blockquote>');
        md = md.replace(/^---+$/gm, '<hr style="border:none;border-top:1px solid #2a3a4e;margin:24px 0">');
        md = md.replace(/^[-*]\s+(.+)$/gm, '\u0000L$1');
        md = md.replace(/^\d+\.\s+(.+)$/gm, '\u0000L$1');
        var parts = md.split(/\n\n+/);
        var out = '';
        for (var i = 0; i < parts.length; i++) {
            var p = parts[i].replace(/\n$/, '').trim();
            if (!p) continue;
            if (p.indexOf('\u0000L') === 0) {
                var items = p.split('\n').filter(function (x) { return x.indexOf('\u0000L') === 0; });
                var lis = items.map(function (x) { return '<li style="margin:4px 0;color:#8896a7">' + x.slice(2) + '</li>'; }).join('');
                out += '<ul style="padding-left:24px;margin:8px 0">' + lis + '</ul>';
            } else if (/^<(h\d|pre|blockquote|hr|ul|ol|table|div|p|img)/i.test(p) || /\u0000I\d+\u0000/.test(p) === false && /^</.test(p)) {
                out += p + '\n';
            } else {
                out += '<p style="margin:8px 0;line-height:1.7;color:#8896a7">' + p.replace(/\n/g, '<br>') + '</p>\n';
            }
        }
        out = out.replace(/\u0000I(\d+)\u0000/g, function (m, j) { return inlines[parseInt(j, 10)]; });
        out = out.replace(/\u0000B(\d+)\u0000/g, function (m, j) { return blocks[parseInt(j, 10)]; });
        return out;
    }

    // Wrap body content in a full dark-theme HTML document with Google Fonts
    function wrapHTMLDoc(body) {
        return '<!DOCTYPE html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1.0">' +
            '<link rel="preconnect" href="https://fonts.googleapis.com">' +
            '<link href="https://fonts.googleapis.com/css2?family=Noto+Serif+SC:wght@400;700;900&family=JetBrains+Mono:wght@400;700&family=Noto+Sans+SC:wght@300;400;500;700&display=swap" rel="stylesheet">' +
            '<style>' + THEME_CSS + '</style></head><body><div class="container">' + body + '</div></body></html>';
    }

    // Turn LLM output (HTML or Markdown) into a full dark-theme HTML document for iframe srcdoc
    function renderSummary(text) {
        var s = (text || '').trim();
        if (!s) return '';
        var m = s.match(/^```(?:html)?\s*\n([\s\S]*?)\n```$/);
        if (m) s = m[1].trim();
        // If full HTML doc, extract body content so we can re-wrap with our theme
        if (/<\/html>/i.test(s) || /^<!doctype/i.test(s)) {
            var bodyMatch = s.match(/<body[^>]*>([\s\S]*?)<\/body>/i);
            if (bodyMatch) s = bodyMatch[1].trim();
        }
        // Strip leftover <style> blocks if LLM included its own
        s = s.replace(/<style[^>]*>[\s\S]*?<\/style>/gi, '');
        // Has HTML tags -> wrap with theme; otherwise convert Markdown
        if (/<(?:div|p|h[1-6]|ul|ol|table|section|article|figure|pre|span|code|strong|em|blockquote)/i.test(s)) {
            return wrapHTMLDoc(s);
        }
        return wrapHTMLDoc(markdownToHTML(s));
    }

    // Copy HTML to clipboard so it can be pasted into Word/Notion/etc as rich content
    function copyHTMLToClipboard(html) {
        if (navigator.clipboard && navigator.clipboard.write && typeof ClipboardItem !== 'undefined') {
            var htmlBlob = new Blob([html], { type: 'text/html' });
            var textBlob = new Blob([html], { type: 'text/plain' });
            try {
                var item = new ClipboardItem({ 'text/html': htmlBlob, 'text/plain': textBlob });
                navigator.clipboard.write([item]).then(function () {
                    log('结果已复制到剪贴板', 'ok');
                }).catch(function () {
                    navigator.clipboard.writeText(html).then(function () { log('结果已复制到剪贴板', 'ok'); });
                });
                return;
            } catch (e) { /* fall through */ }
        }
        if (navigator.clipboard && navigator.clipboard.writeText) {
            navigator.clipboard.writeText(html).then(function () { log('结果已复制到剪贴板', 'ok'); });
        } else {
            fallbackCopy(html);
            log('结果已复制到剪贴板', 'ok');
        }
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
                var html = renderSummary(summary);
                log('AI\u603b\u7ed3\u5df2\u751f\u6210:', 'ok');
                var logEl = root.querySelector('#bs-log');
                if (logEl) {
                    var wrap = document.createElement("div");
                    wrap.style.cssText = 'position:relative;margin-top:8px;border:1px solid #cba6f7;border-radius:6px;overflow:hidden';
                    var copyBtn = document.createElement("button");
                    copyBtn.title = '一键复制结果';
                    var copyIconSVG = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><rect x="8" y="2" width="8" height="4" rx="1" ry="1"/></svg>';
                    var checkIconSVG = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
                    copyBtn.innerHTML = copyIconSVG;
                    copyBtn.style.cssText = 'position:absolute;top:6px;right:6px;z-index:10;background:#313244;border:1px solid #45475a;border-radius:4px;color:#cdd6f4;width:28px;height:28px;padding:0;cursor:pointer;display:flex;align-items:center;justify-content:center';
                    copyBtn.onclick = function () {
                        copyHTMLToClipboard(html);
                        copyBtn.innerHTML = checkIconSVG;
                        copyBtn.style.color = '#a6e3a1';
                        copyBtn.style.borderColor = '#a6e3a1';
                        setTimeout(function () {
                            copyBtn.innerHTML = copyIconSVG;
                            copyBtn.style.color = '';
                            copyBtn.style.borderColor = '';
                        }, 1500);
                    };
                    wrap.appendChild(copyBtn);
                    var frame = document.createElement('iframe');
                    frame.style.cssText = 'width:100%;height:420px;border:none;background:#fff;display:block';
                    frame.srcdoc = html;
                    frame.onload = function () {
                        try {
                            var h = frame.contentWindow.document.body.scrollHeight;
                            frame.style.height = Math.min(h + 24, 600) + 'px';
                        } catch (e) {}
                    };
                    wrap.appendChild(frame);
                    logEl.appendChild(wrap);
                    logEl.scrollTop = logEl.scrollHeight;
                }
                copyHTMLToClipboard(html);
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
