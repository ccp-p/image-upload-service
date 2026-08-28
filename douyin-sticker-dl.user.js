// ==UserScript==
// @name         抖音评论GIF表情一键下载
// @namespace    https://docs.scriptcat.org/
// @version      0.1.0
// @description  在抖音评论表情图片上添加下载按钮，一键保存真实GIF到本地相册
// @author       You
// @match        https://*.douyin.com/*
// @match        https://www.douyin.com/*
// @match        https://douyin.com/*
// @grant        GM_xmlhttpRequest
// @connect      douyinpic.com
// @connect      *.douyinpic.com
// @noframes
// @run-at       document-idle
// ==/UserScript==

(function () {
    'use strict';

    // ==================== 常量 ====================
    var STICKER_SELECTOR = 'img[data-sticker-handled="true"]';
    var BTN_CLASS = 'dy-sticker-dl-btn';
    var BTN_SIZE = 32;
    var PROCESSED_ATTR = 'data-dy-dl-added';

    // ==================== Toast ====================
    function showToast(msg, isError) {
        var old = document.getElementById('dy-sticker-dl-toast');
        if (old) old.remove();
        var t = document.createElement('div');
        t.id = 'dy-sticker-dl-toast';
        t.textContent = msg;
        Object.assign(t.style, {
            position: 'fixed', bottom: '80px', left: '50%', transform: 'translateX(-50%)',
            zIndex: '2147483647', padding: '10px 20px', maxWidth: '80vw',
            backgroundColor: isError ? 'rgba(220,38,38,0.92)' : 'rgba(22,163,74,0.92)',
            color: '#fff', fontSize: '14px', fontWeight: '600', borderRadius: '20px',
            boxShadow: '0 4px 16px rgba(0,0,0,0.25)', pointerEvents: 'none',
            whiteSpace: 'nowrap', textAlign: 'center'
        });
        document.body.appendChild(t);
        setTimeout(function () { t.remove(); }, 2500);
    }

    // ==================== 格式检测 ====================
    function detectFormat(bytes) {
        if (bytes.length < 12) return 'bin';
        var b0 = bytes[0], b1 = bytes[1], b2 = bytes[2];
        if (b0 === 0x47 && b1 === 0x49 && b2 === 0x46) return 'gif';
        if (b0 === 0x52 && b1 === 0x49 && b2 === 0x46 &&
            bytes[8] === 0x57 && bytes[9] === 0x45 && bytes[10] === 0x42 && bytes[11] === 0x50) return 'webp';
        if (b0 === 0x89 && b1 === 0x50 && b2 === 0x4E) return 'png';
        if (b0 === 0xFF && b1 === 0xD8) return 'jpg';
        return 'bin';
    }

    function mimeFor(fmt) {
        return { gif: 'image/gif', webp: 'image/webp', png: 'image/png', jpg: 'image/jpeg' }[fmt] || 'application/octet-stream';
    }

    // ==================== URL 候选 ====================
    function getGifCandidates(src) {
        var urls = [];
        try {
            var u = new URL(src);
            // 优先尝试 sc=sticker_heif -> sc=sticker_gif
            if (u.searchParams.has('sc')) {
                var scVal = u.searchParams.get('sc');
                if (scVal && scVal.indexOf('heif') !== -1) {
                    u.searchParams.set('sc', scVal.replace('heif', 'gif'));
                    urls.push(u.href);
                }
            }
        } catch (e) { /* URL 解析失败时只用原始地址 */ }
        urls.push(src);
        return urls;
    }

    // ==================== 下载 ====================
    function gmFetch(url) {
        return new Promise(function (resolve, reject) {
            if (typeof GM_xmlhttpRequest !== 'function') {
                // 兜底：无 GM 环境时用 fetch（可能受 CORS 限制）
                fetch(url, { credentials: 'include', mode: 'cors' }).then(function (res) {
                    if (!res.ok) throw new Error('HTTP ' + res.status);
                    return res.arrayBuffer();
                }).then(resolve).catch(reject);
                return;
            }
            GM_xmlhttpRequest({
                method: 'GET',
                url: url,
                responseType: 'arraybuffer',
                onload: function (res) {
                    if (res.status >= 200 && res.status < 300) {
                        resolve(res.response);
                    } else {
                        reject(new Error('HTTP ' + res.status));
                    }
                },
                onerror: function () { reject(new Error('network error')); },
                ontimeout: function () { reject(new Error('timeout')); }
            });
        });
    }

    function triggerDownload(blob, filename) {
        var a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = filename;
        a.style.display = 'none';
        document.body.appendChild(a);
        a.click();
        setTimeout(function () {
            URL.revokeObjectURL(a.href);
            a.remove();
        }, 1000);
    }

    function setBtnLoading(btn, loading) {
        if (!btn) return;
        btn.style.opacity = loading ? '0.5' : '1';
        btn.style.pointerEvents = loading ? 'none' : 'auto';
    }

    function downloadSticker(img, btn) {
        var src = img.src || img.currentSrc;
        if (!src) { showToast('未找到图片地址', true); return; }

        var urls = getGifCandidates(src);
        var idx = 0;

        function tryNext() {
            if (idx >= urls.length) {
                showToast('下载失败，所有候选地址均不可用', true);
                setBtnLoading(btn, false);
                return;
            }
            var url = urls[idx++];
            gmFetch(url).then(function (buf) {
                var bytes = new Uint8Array(buf);
                var fmt = detectFormat(bytes);
                var mime = mimeFor(fmt);
                var blob = new Blob([bytes], { type: mime });
                var name = 'douyin_sticker_' + Date.now() + '.' + fmt;
                triggerDownload(blob, name);
                showToast('已保存 ' + fmt.toUpperCase() + ' (' + (bytes.length / 1024).toFixed(1) + ' KB)');
                setBtnLoading(btn, false);
            }).catch(function () {
                tryNext();
            });
        }

        setBtnLoading(btn, true);
        tryNext();
    }

    // ==================== 按钮注入 ====================
    function findPositionedHost(img) {
        var el = img.parentElement;
        while (el && el !== document.body) {
            var pos = getComputedStyle(el).position;
            if (pos === 'relative' || pos === 'absolute' || pos === 'fixed') return el;
            el = el.parentElement;
        }
        // 兜底：将直接父元素设为 relative
        var p = img.parentElement;
        if (p) {
            p.style.position = 'relative';
            return p;
        }
        return null;
    }

    function createButton(img) {
        if (img.getAttribute(PROCESSED_ATTR)) return;
        img.setAttribute(PROCESSED_ATTR, '1');

        // 清理同容器中可能残留的旧按钮
        var host = findPositionedHost(img);
        if (!host) return;
        var oldBtns = host.querySelectorAll('.' + BTN_CLASS);
        for (var i = 0; i < oldBtns.length; i++) oldBtns[i].remove();

        var btn = document.createElement('div');
        btn.className = BTN_CLASS;
        btn.innerHTML = '<svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>';

        Object.assign(btn.style, {
            position: 'absolute',
            width: BTN_SIZE + 'px',
            height: BTN_SIZE + 'px',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            borderRadius: '50%',
            background: 'rgba(0,0,0,0.55)',
            color: '#fff',
            cursor: 'pointer',
            zIndex: '9999',
            backdropFilter: 'blur(4px)',
            WebkitBackdropFilter: 'blur(4px)',
            transition: 'opacity 0.15s, transform 0.15s',
            touchAction: 'manipulation',
            userSelect: 'none',
            WebkitUserSelect: 'none',
            WebkitTouchCallout: 'none'
        });
        btn.title = '下载GIF';

        function position() {
            var imgRect = img.getBoundingClientRect();
            var hostRect = host.getBoundingClientRect();
            btn.style.display = 'flex';
            var right = hostRect.right - imgRect.right + 4;
            var bottom = hostRect.bottom - imgRect.bottom + 4;
            btn.style.right = right + 'px';
            btn.style.bottom = bottom + 'px';
        }

        position();
        host.appendChild(btn);

        // 图片异步加载后尺寸变化，重新定位
        img.addEventListener('load', position);

        btn.addEventListener('click', function (e) {
            e.preventDefault();
            e.stopPropagation();
            downloadSticker(img, btn);
        });

        btn.addEventListener('touchstart', function (e) {
            e.stopPropagation();
        }, { passive: true });

        window.addEventListener('resize', position, { passive: true });
    }

    // ==================== 扫描 & Observer ====================
    function scan() {
        var imgs = document.querySelectorAll(STICKER_SELECTOR);
        for (var i = 0; i < imgs.length; i++) {
            createButton(imgs[i]);
        }
    }

    var observer = new MutationObserver(function () {
        scan();
    });

    function init() {
        scan();
        observer.observe(document.body, { childList: true, subtree: true });
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
