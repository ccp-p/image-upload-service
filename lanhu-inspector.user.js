// ==UserScript==
// @name         Lanhu LayerBox Inspector
// @namespace    https://docs.scriptcat.org/
// @version      3.0.0
// @description  v3.0: 扁平设计稿自动重建嵌套树+流式布局，仅越界/叠加装饰元素用absolute
// @author       You
// @match        https://lanhuapp.com/web/*
// @grant        none
// @noframes
// ==/UserScript==

(function () {
    'use strict';

    var TARGET_COMPONENT_NAME = 'Layers';
    var AMFE_FLEXIBLE_CDN = 'https://cdn.jsdelivr.net/npm/amfe-flexible/index.js';
    var STORAGE_KEY_MODE = 'lanhu_inspector_mode';
    var STORAGE_KEY_REM = 'lanhu_inspector_rem_base';
    var STORAGE_KEY_BIND = 'lanhu_inspector_bind_framework';
    var CONTAIN_TOL = 2; // 包含判定容错px

    var REM_OPTIONS = [
        { value: '375', label: '37.5px (375稿)' },
        { value: '750', label: '75px (750稿)' },
        { value: '1000', label: '100px (1000稿)' }
    ];
    var BIND_OPTIONS = [
        { value: 'jquery', label: '🔗 jQuery' },
        { value: 'vue', label: '💚 Vue' }
    ];

    function getRemDivisor(w) { return Number(w) / 10; }

    // ==================== VNode 遍历 & 查找 ====================
    function collectVNodeInstances(vnode, queue, visited) {
        if (!vnode) return;
        if (vnode.componentInstance && !visited.has(vnode.componentInstance)) queue.push(vnode.componentInstance);
        if (Array.isArray(vnode.children)) {
            for (var i = 0; i < vnode.children.length; i++) collectVNodeInstances(vnode.children[i], queue, visited);
        }
        if (vnode.componentInstance && vnode.componentInstance._vnode) {
            collectVNodeInstances(vnode.componentInstance._vnode, queue, visited);
        }
    }

    function findVmByNameAnywhere(root, targetName) {
        var queue = [root], visited = new WeakSet();
        while (queue.length) {
            var vm = queue.shift();
            if (!vm || visited.has(vm)) continue;
            visited.add(vm);
            var name = vm.$options.name || vm.$options._componentTag;
            if (name === targetName) return vm;
            if (vm.$children) {
                for (var j = 0; j < vm.$children.length; j++) {
                    if (!visited.has(vm.$children[j])) queue.push(vm.$children[j]);
                }
            }
            if (vm._vnode) collectVNodeInstances(vm._vnode, queue, visited);
        }
        return null;
    }

    // ==================== Toast ====================
    function showToast(msg, isError) {
        var old = document.getElementById('layer-inspector-toast');
        if (old) old.remove();
        var t = document.createElement('div');
        t.id = 'layer-inspector-toast';
        t.textContent = msg;
        Object.assign(t.style, {
            position: 'fixed', top: '20px', left: '50%', transform: 'translateX(-50%)', zIndex: '9999999',
            padding: '12px 24px', maxWidth: '80vw', backgroundColor: isError ? '#ff4d4f' : '#52c41a',
            color: '#fff', fontSize: '15px', fontWeight: 'bold', borderRadius: '8px',
            boxShadow: '0 4px 12px rgba(0,0,0,0.2)', transition: 'opacity 0.3s', opacity: '1',
            pointerEvents: 'none', whiteSpace: 'pre-wrap', textAlign: 'center'
        });
        document.body.appendChild(t);
        setTimeout(function () { t.style.opacity = '0'; setTimeout(function () { t.remove(); }, 300); }, 3000);
    }

    // ==================== 数据提取 ====================
    function num(v) { return (v != null && v !== '') ? Number(v) : undefined; }
    function isTextType(layer) { return layer.type === 'text' || layer.type === 'textLayer'; }
    function getX(layer) { return layer.x != null ? layer.x : (layer.left != null ? layer.left : undefined); }
    function getY(layer) { return layer.y != null ? layer.y : (layer.top != null ? layer.top : undefined); }

    function extractImageUrl(layer) {
        if (layer.backgroundImage) return layer.backgroundImage;
        if (layer.bgImage) return layer.bgImage;
        if (layer.imageUrl) return layer.imageUrl;
        if (layer.imgUrl) return layer.imgUrl;
        if (layer.image) return layer.image;
        if (layer.fill && layer.fill[0] && layer.fill[0].url) return layer.fill[0].url;
        if (layer.fills && layer.fills[0] && layer.fills[0].url) return layer.fills[0].url;
        if (layer.images) {
            var keys = ['png_xxxhd', 'png_xxhd', 'png_xhd', 'png_hd', 'png'];
            for (var k = 0; k < keys.length; k++) {
                var val = layer.images[keys[k]];
                if (val && typeof val === 'string') return val;
            }
        }
        return undefined;
    }

    function hasVisualContent(layer, sliceIds) {
        return isTextType(layer) || !!layer.backgroundColor || !!extractImageUrl(layer)
            || (layer.borderRadius > 0) || (sliceIds.indexOf(layer.web_id) !== -1);
    }

    // ==================== 几何工具 ====================
    function boxOf(l) { return { x: getX(l) || 0, y: getY(l) || 0, w: l.width || 0, h: l.height || 0 }; }
    function boxArea(b) { return (b.w || 0) * (b.h || 0); }
    function contains(outer, inner, tol) {
        tol = tol || 0;
        return inner.x >= outer.x - tol
            && inner.y >= outer.y - tol
            && (inner.x + inner.w) <= (outer.x + outer.w) + tol
            && (inner.y + inner.h) <= (outer.y + outer.h) + tol;
    }
    function boxIntersect(a, b) {
        var ix = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
        var iy = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
        return ix > 0 && iy > 0;
    }
    function overlapArea(a, b) {
        var ix = Math.max(0, Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x));
        var iy = Math.max(0, Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y));
        return ix * iy;
    }
    function arrMax(a) { var m = null; for (var i = 0; i < a.length; i++) { if (m == null || a[i] > m) m = a[i]; } return m == null ? 0 : m; }
    function arrMin(a) { var m = null; for (var i = 0; i < a.length; i++) { if (m == null || a[i] < m) m = a[i]; } return m == null ? 0 : m; }
    function median(a) { if (!a.length) return 0; var s = a.slice().sort(function (x, y) { return x - y; }); var m = Math.floor(s.length / 2); return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2; }

    // ==================== 对齐推断 ====================
    function crossAlign(parentBox, childBoxes, axis, sizeKey) {
        var pSize = parentBox[sizeKey];
        if (!pSize) return 'center';
        var pCenter = parentBox[axis] + pSize / 2;
        var sum = 0;
        for (var i = 0; i < childBoxes.length; i++) sum += (childBoxes[i][axis] + childBoxes[i][sizeKey] / 2 - pCenter);
        var avg = sum / childBoxes.length;
        var tol = pSize * 0.12;
        if (Math.abs(avg) < tol) return 'center';
        return avg < 0 ? 'flex-start' : 'flex-end';
    }
    function mainJustify(parentBox, childBoxes, isColumn) {
        var axis = isColumn ? 'y' : 'x';
        var sizeKey = isColumn ? 'h' : 'w';
        var pSize = parentBox[sizeKey];
        if (!pSize || !childBoxes.length) return 'flex-start';
        var sorted = childBoxes.slice().sort(function (a, b) { return a[axis] - b[axis]; });
        var first = sorted[0][axis];
        var last = sorted[sorted.length - 1];
        var lastEnd = last[axis] + last[sizeKey];
        var head = first - parentBox[axis];
        var tail = (parentBox[axis] + pSize) - lastEnd;
        var tol = pSize * 0.12;
        if (sorted.length >= 2 && Math.abs(head) < tol && Math.abs(tail) < tol) return 'space-between';
        var mid = (first + lastEnd) / 2;
        if (Math.abs(mid - (parentBox[axis] + pSize / 2)) < tol) return 'center';
        if (head < tol) return 'flex-start';
        return 'flex-end';
    }

    // ==================== 核心：扁平数组 → 包含关系树 ====================
    function buildContainmentTree(layers, sliceIds) {
        var visual = [];
        for (var i = 0; i < layers.length; i++) {
            if (hasVisualContent(layers[i], sliceIds)) visual.push(layers[i]);
        }
        var boxes = visual.map(boxOf);

        // 找完全包含它的最小容器
        function findContainParent(idx) {
            var child = boxes[idx];
            var bestJ = -1, bestArea = Infinity;
            for (var j = 0; j < boxes.length; j++) {
                if (j === idx) continue;
                if (contains(boxes[j], child, CONTAIN_TOL)) {
                    var a = boxArea(boxes[j]);
                    if (a < bestArea) { bestArea = a; bestJ = j; }
                }
            }
            return bestJ;
        }
        // 无包含者 → 找重叠面积最大的更大容器
        function findOverlapParent(idx) {
            var child = boxes[idx];
            var bestJ = -1, bestOv = 0;
            for (var j = 0; j < boxes.length; j++) {
                if (j === idx) continue;
                if (boxArea(boxes[j]) <= boxArea(child)) continue;
                var ov = overlapArea(boxes[j], child);
                if (ov > bestOv) { bestOv = ov; bestJ = j; }
            }
            return bestJ;
        }
        // 仍无 → 找 x方向包含该元素中心的、面积最大的容器(越界装饰元素挂到最大容器)
        function findNearestParent(idx) {
            var child = boxes[idx];
            var cx = child.x + child.w / 2;
            var bestJ = -1, bestArea = 0;
            for (var j = 0; j < boxes.length; j++) {
                if (j === idx) continue;
                if (boxArea(boxes[j]) <= boxArea(child)) continue;
                if (cx >= boxes[j].x && cx <= boxes[j].x + boxes[j].w) {
                    if (boxArea(boxes[j]) > bestArea) { bestArea = boxArea(boxes[j]); bestJ = j; }
                }
            }
            // fallback: 取面积最大的更大容器
            if (bestJ === -1) {
                for (var j2 = 0; j2 < boxes.length; j2++) {
                    if (j2 === idx) continue;
                    if (boxArea(boxes[j2]) <= boxArea(child)) continue;
                    if (boxArea(boxes[j2]) > bestArea) { bestArea = boxArea(boxes[j2]); bestJ = j2; }
                }
            }
            return bestJ;
        }

        var nodes = [];
        for (var k = 0; k < visual.length; k++) {
            var p = findContainParent(k);
            if (p === -1) p = findOverlapParent(k);
            if (p === -1) p = findNearestParent(k);
            nodes.push({ layer: visual[k], box: boxes[k], children: [], abs: false, parentIdx: p });
        }

        var roots = [];
        for (var m = 0; m < nodes.length; m++) {
            var pi = nodes[m].parentIdx;
            if (pi >= 0) {
                nodes[pi].children.push(nodes[m]);
                // 非完全包含(靠重叠/距离挂载) → 标记 abs
                if (!contains(nodes[pi].box, nodes[m].box, CONTAIN_TOL)) nodes[m].abs = true;
            } else {
                roots.push(nodes[m]);
            }
        }
        return roots;
    }

    // ==================== 横向行分组 ====================
    function groupRows(nodes) {
        var sorted = nodes.slice().sort(function (a, b) { return a.box.y - b.box.y; });
        var rows = [];
        for (var i = 0; i < sorted.length; i++) {
            var n = sorted[i];
            var placed = false;
            for (var r = 0; r < rows.length && !placed; r++) {
                for (var k = 0; k < rows[r].length; k++) {
                    var rb = rows[r][k].box;
                    if (n.box.y < rb.y + rb.h && rb.y < n.box.y + n.box.h) {
                        rows[r].push(n);
                        placed = true;
                        break;
                    }
                }
            }
            if (!placed) rows.push([n]);
        }
        rows.forEach(function (row) { row.sort(function (a, b) { return a.box.x - b.box.x; }); });
        return rows;
    }

    function computeGap(nodes, axis, sizeKey) {
        if (nodes.length < 2) return 0;
        var sorted = nodes.slice().sort(function (a, b) { return a.box[axis] - b.box[axis]; });
        var gaps = [];
        for (var i = 1; i < sorted.length; i++) {
            gaps.push(sorted[i].box[axis] - (sorted[i - 1].box[axis] + sorted[i - 1].box[sizeKey]));
        }
        var vg = gaps.filter(function (v) { return v >= 0; });
        return vg.length ? median(vg) : 0;
    }

    function computeRowGap(rows) {
        if (rows.length < 2) return 0;
        var gaps = [];
        for (var i = 1; i < rows.length; i++) {
            var prevBoxes = rows[i - 1].map(function (n) { return n.box; });
            var currBoxes = rows[i].map(function (n) { return n.box; });
            var prevBottom = arrMax(prevBoxes.map(function (b) { return b.y + b.h; }));
            var currTop = arrMin(currBoxes.map(function (b) { return b.y; }));
            gaps.push(currTop - prevBottom);
        }
        var vg = gaps.filter(function (v) { return v >= 0; });
        return vg.length ? Math.round(median(vg)) : 0;
    }

    function makeVirtualRow(rowNodes, idx) {
        var boxes = rowNodes.map(function (n) { return n.box; });
        var minX = arrMin(boxes.map(function (b) { return b.x; }));
        var minY = arrMin(boxes.map(function (b) { return b.y; }));
        var maxX = arrMax(boxes.map(function (b) { return b.x + b.w; }));
        var maxY = arrMax(boxes.map(function (b) { return b.y + b.h; }));
        var vbox = { x: minX, y: minY, w: maxX - minX, h: maxY - minY };
        return {
            virtual: true,
            n: 'auto-row-' + idx,
            box: vbox,
            layout: 'row',
            gap: Math.round(computeGap(rowNodes, 'x', 'w')),
            align: crossAlign(vbox, boxes, 'y', 'h'),
            justify: mainJustify(vbox, boxes, false),
            children: rowNodes
        };
    }

    // ==================== 递归处理布局 ====================
    function processNode(node) {
        var childNodes = node.children;
        if (!childNodes || !childNodes.length) return;

        for (var i = 0; i < childNodes.length; i++) processNode(childNodes[i]);

        // 相交(非包含) → 较小者 abs
        for (var i2 = 0; i2 < childNodes.length; i2++) {
            if (childNodes[i2].abs) continue;
            for (var j = i2 + 1; j < childNodes.length; j++) {
                if (childNodes[j].abs) continue;
                if (boxIntersect(childNodes[i2].box, childNodes[j].box)) {
                    if (boxArea(childNodes[i2].box) <= boxArea(childNodes[j].box)) childNodes[i2].abs = true;
                    else childNodes[j].abs = true;
                }
            }
        }

        // abs 元素补 rx/ry（相对直接父容器）
        for (var k = 0; k < childNodes.length; k++) {
            if (childNodes[k].abs) {
                childNodes[k].rx = childNodes[k].box.x - node.box.x;
                childNodes[k].ry = childNodes[k].box.y - node.box.y;
            }
        }

        var flow = [], absList = [];
        for (var m = 0; m < childNodes.length; m++) {
            if (childNodes[m].abs) absList.push(childNodes[m]);
            else flow.push(childNodes[m]);
        }

        if (!flow.length) { node.layout = 'overlap'; return; }

        var rows = groupRows(flow);

        if (rows.length === 1 && rows[0].length === 1) {
            node.layout = 'single';
            node.align = crossAlign(node.box, [rows[0][0].box], 'x', 'w');
        } else if (rows.length === 1) {
            node.layout = 'row';
            var rboxes = rows[0].map(function (n) { return n.box; });
            node.gap = Math.round(computeGap(rows[0], 'x', 'w'));
            node.align = crossAlign(node.box, rboxes, 'y', 'h');
            node.justify = mainJustify(node.box, rboxes, false);
        } else {
            node.layout = 'column';
            var flowBoxes = flow.map(function (n) { return n.box; });
            node.gap = computeRowGap(rows);
            node.align = crossAlign(node.box, flowBoxes, 'x', 'w');
            node.justify = mainJustify(node.box, flowBoxes, true);
            // 多元素行包装为 row 虚拟容器
            var newChildren = [];
            var rc = 0;
            for (var r = 0; r < rows.length; r++) {
                if (rows[r].length === 1) newChildren.push(rows[r][0]);
                else { rc++; newChildren.push(makeVirtualRow(rows[r], rc)); }
            }
            for (var a = 0; a < absList.length; a++) newChildren.push(absList[a]);
            node.children = newChildren;
        }
    }

    function inferTopLayout(roots) {
        if (!roots || roots.length <= 1) return null;
        var boxes = roots.map(function (r) { return r.box; });
        var ys = boxes.map(function (b) { return b.y; });
        var xs = boxes.map(function (b) { return b.x; });
        var isColumn = (arrMax(ys) - arrMin(ys)) >= (arrMax(xs) - arrMin(xs));
        var sorted = boxes.slice().sort(function (a, b) { return isColumn ? a.y - b.y : a.x - b.x; });
        var gaps = [];
        for (var i = 1; i < sorted.length; i++) {
            gaps.push(isColumn ? (sorted[i].y - (sorted[i - 1].y + sorted[i - 1].h)) : (sorted[i].x - (sorted[i - 1].x + sorted[i - 1].w)));
        }
        var vg = gaps.filter(function (v) { return v >= 0; });
        return { layout: isColumn ? 'column' : 'row', gap: vg.length ? Math.round(median(vg)) : 0 };
    }

    // ==================== 输出节点(不含 x/y 绝对坐标) ====================
    function pickNode(treeNode, sliceIds) {
        if (treeNode.virtual) {
            var vo = { n: treeNode.n, layout: treeNode.layout, virtual: true };
            if (treeNode.gap != null) vo.gap = treeNode.gap;
            if (treeNode.align) vo.align = treeNode.align;
            if (treeNode.justify) vo.justify = treeNode.justify;
            vo.children = (treeNode.children || []).map(function (c) { return pickNode(c, sliceIds); });
            return vo;
        }
        var layer = treeNode.layer;
        var o = {};
        if (layer.width != null) o.w = num(layer.width);
        if (layer.height != null) o.h = num(layer.height);
        var imgUrl = extractImageUrl(layer);
        if (imgUrl) {
            o.imageUrl = imgUrl;
            if (layer.backgroundColor) o.bg = layer.backgroundColor;
            if (layer.borderRadius > 0) o.br = num(layer.borderRadius);
        } else {
            if (isTextType(layer)) {
                o.text = layer.content || layer.text || '';
                if (layer.fontSize != null) o.fs = num(layer.fontSize);
                if (layer.lineHeight != null) o.lh = num(layer.lineHeight);
                if (layer.fontFamily) o.ff = layer.fontFamily;
                if (layer.fontWeight) o.fw = layer.fontWeight;
                if (layer.fontStyle) o.fst = layer.fontStyle;
                if (layer.textAlign) o.ta = layer.textAlign;
                if (layer.letterSpacing != null && layer.letterSpacing !== 0) o.ls = num(layer.letterSpacing);
                if (layer.textDecoration) o.td = layer.textDecoration;
            }
            if (layer.color) o.c = layer.color;
            if (layer.backgroundColor) o.bg = layer.backgroundColor;
            if (layer.borderRadius > 0) o.br = num(layer.borderRadius);
        }
        if (sliceIds && sliceIds.indexOf(layer.web_id) !== -1) o.slice = true;
        o.n = layer.name;
        o.id = layer.web_id;
        if (treeNode.layout) {
            o.layout = treeNode.layout;
            if (treeNode.gap != null) o.gap = treeNode.gap;
            if (treeNode.align) o.align = treeNode.align;
            if (treeNode.justify) o.justify = treeNode.justify;
        }
        if (treeNode.abs) {
            o.abs = true;
            if (treeNode.rx != null) o.rx = treeNode.rx;
            if (treeNode.ry != null) o.ry = treeNode.ry;
        }
        if (treeNode.children && treeNode.children.length) {
            o.children = treeNode.children.map(function (c) { return pickNode(c, sliceIds); });
        }
        return o;
    }

    function buildTree(layersRaw, sliceIds) {
        if (!layersRaw) return { roots: [], top: null };
        var list = Array.isArray(layersRaw) ? layersRaw : [layersRaw];
        var roots = buildContainmentTree(list, sliceIds || []);
        for (var i = 0; i < roots.length; i++) processNode(roots[i]);
        return {
            roots: roots.map(function (r) { return pickNode(r, sliceIds || []); }),
            top: inferTopLayout(roots)
        };
    }

    // ==================== Prompt 生成 ====================
    function buildPrompt(layersRaw, sliceIds, debugMode, designWidth, bindFramework) {
        var divisor = getRemDivisor(designWidth);
        var built = buildTree(layersRaw, sliceIds || []);
        var tree = built.roots;
        var topLayout = built.top;
        var fwLabel = bindFramework === 'vue' ? 'Vue (@click)' : 'jQuery (.on("click"))';

        var lines = [];
        lines.push('# 移动端HTML搭建数据（流式布局）');
        lines.push('⚠️ 所有数值字段均为原始px值(来自' + designWidth + 'px设计稿)');
        lines.push('⚠️ 换算公式: rem = px ÷ ' + divisor + '，保留两位小数');
        lines.push('⚠️ 不要用多余的css3动画和css变量，不要写行内样式');
        lines.push('⚠️ 页面使用 amfe-flexible.js(' + designWidth + 'px设计稿, 1rem=' + divisor + 'px)');
        lines.push('');
        lines.push('## ⚠️ 核心规则：流式布局优先（必须遵守）');
        lines.push('- 数据已从扁平设计稿自动重建为【嵌套树】，children 必须嵌套为 DOM 子元素');
        lines.push('- 所有容器默认 display:flex，方向由 layout 字段决定：');
        lines.push('  layout=column → flex-direction:column（纵向堆叠）');
        lines.push('  layout=row → flex-direction:row（横向排列）');
        lines.push('  layout=single → 单子元素容器，用 flex 居中该子元素');
        lines.push('  layout=overlap → 子元素互相叠加，父容器 position:relative，子元素用 absolute');
        lines.push('- 子元素间距用 CSS gap（取 gap 字段，px÷' + divisor + ' 换算 rem）');
        lines.push('- 交叉轴对齐用 align-items（取 align 字段：center/flex-start/flex-end）');
        lines.push('- 主轴对齐用 justify-content（取 justify 字段）');
        lines.push('- ⚠️ 仅 abs:true 的元素用 position:absolute（相对直接父容器），left/top 取 rx/ry 换算 rem；其余一律流式，禁止用 left/top');
        lines.push('- ⚠️ 数据中无 x/y 绝对坐标，禁止自行估算定位；元素尺寸用 w/h 换算');
        lines.push('- virtual:true 是自动分组的横向行容器，生成普通 <div> 加 row 布局即可，无需额外文字');
        lines.push('');
        lines.push('## 图片渲染');
        lines.push('- 所有含 imageUrl 的节点用 <div> + CSS background-image: url(...) 渲染');
        lines.push('- 禁止使用 <img> 标签，background-size 必须为 100% 100%');
        lines.push('- width/height 取节点 w/h 换算 rem');
        lines.push('- ⚠️ 含 imageUrl 的节点已剔除 text 字段，图片本身含文字，不要再额外加文字元素');
        lines.push('');
        lines.push('## CSS规范');
        lines.push('- 禁止写行内样式(style属性)，所有样式通过CSS类名实现');
        lines.push('- 根据节点 n(name) 字段生成语义化类名（如 .header-title, .banner-img）');
        lines.push('- CSS层级扁平，最多2层嵌套');
        lines.push('');
        lines.push('### 事件绑定规则(' + fwLabel + ')（严格执行）');
        lines.push('- 每个可交互元素独立注册点击事件，禁止全局代理');
        lines.push('- 事件处理函数内【只允许】写一行 console.log("节点名 clicked")');
        lines.push('- 禁止添加任何TODO注释、业务逻辑、跳转、状态修改等额外代码');
        if (bindFramework === 'vue') {
            lines.push('- 示例: @click="handleBannerImgClick" → handleBannerImgClick() { console.log("banner-img clicked"); }');
        } else {
            lines.push('- 示例: $(".banner-img").on("click", function(e) { console.log("banner-img clicked"); });');
        }
        lines.push('');
        lines.push('## 字段映射');
        lines.push('w/h=尺寸(px) | layout=布局(column/row/single/overlap) | gap=同层间距(px) | align=交叉轴对齐 | justify=主轴对齐');
        lines.push('abs=需绝对定位 | rx/ry=abs元素相对父容器偏移(px) | virtual=自动行容器');
        lines.push('text=文本 | fs/lh=字号行高 | ff/fw/fst=字体 | ls/td/ta=字距/装饰/对齐');
        lines.push('c/bg/br=颜色/背景/圆角 | imageUrl=图片URL | slice=切图标记');
        lines.push('n=节点名称(生成类名) | children=子节点(必须嵌套为DOM子元素)');
        lines.push('');
        lines.push('## 输出格式');
        if (debugMode) {
            lines.push('- 【调试模式】输出完整单文件HTML Demo(<!DOCTYPE html>到</html>)');
            lines.push('- <head>引入 <script src="' + AMFE_FLEXIBLE_CDN + '"></script>');
            lines.push('- CSS在<style>内，JS在<script>内，DOM/CSS/JS在同一文件');
            lines.push('- 【调试模式】缺失数据用合理默认值，不提问');
        } else {
            lines.push('- HTML/CSS/JS 分别用 ```html / ```css / ```javascript 代码块包裹');
            lines.push('- 数据缺失/歧义时必须提问确认，禁止猜测');
        }
        lines.push('');
        if (topLayout) {
            lines.push('## 顶层布局');
            lines.push(JSON.stringify(topLayout));
            lines.push('');
        }
        lines.push('## 节点树（按此嵌套结构构建DOM，流式布局）');
        lines.push(JSON.stringify(tree));
        return lines.join('\n');
    }

    // ==================== 主流程 ====================
    function doSearch(debugMode, designWidth, bindFramework) {
        var appEl = document.body.querySelector('[data-app]');
        var root = appEl && appEl.__vue__;
        if (!root) { showToast('❌ 未检测到Vue实例', true); return; }
        var vm = findVmByNameAnywhere(root, TARGET_COMPONENT_NAME);
        if (!vm) { showToast('❌ 未找到Layers组件', true); return; }
        var layers = vm.$props && vm.$props.layers;
        var sliceIds = (vm.$data && vm.$data.selectedSliceIdArr) || [];
        if (!layers) { showToast('⚠️ layers为空，请先打开设计稿', true); return; }
        var text = buildPrompt(layers, sliceIds, debugMode, designWidth, bindFramework);
        var divisor = getRemDivisor(designWidth);
        var fwLabel = bindFramework === 'vue' ? 'Vue' : 'jQuery';
        var modeLabel = debugMode ? '🐛调试' : '📋正常';
        try {
            navigator.clipboard.writeText(text).then(function () {
                showToast(modeLabel + ' | ' + divisor + 'px | ' + fwLabel + ' | 已复制 ' + text.length + ' 字符');
                console.warn('[LayerInspector] 预览:', text);
            }).catch(function () { fallbackCopy(text, modeLabel, divisor, fwLabel); });
        } catch (e) { fallbackCopy(text, modeLabel, divisor, fwLabel); }
    }

    function fallbackCopy(text, modeLabel, divisor, fwLabel) {
        var ta = document.createElement('textarea');
        ta.value = text;
        ta.style.cssText = 'position:fixed;left:-9999px';
        document.body.appendChild(ta); ta.select(); document.execCommand('copy'); ta.remove();
        showToast(modeLabel + ' | ' + divisor + 'px | ' + fwLabel + ' | 已复制(fallback)');
        console.warn('[LayerInspector] 预览:', text);
    }

    // ==================== UI ====================
    function createUI() {
        if (document.getElementById('layer-inspector-wrapper')) return;
        var wrapper = document.createElement('div');
        wrapper.id = 'layer-inspector-wrapper';
        Object.assign(wrapper.style, {
            position: 'fixed', bottom: '20px', right: '20px', zIndex: '999999',
            display: 'flex', alignItems: 'center', gap: '6px',
            fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif'
        });
        var ss = {
            padding: '10px 8px', borderRadius: '8px', border: 'none',
            backgroundColor: '#fff', color: '#333', fontSize: '12px',
            boxShadow: '0 2px 8px rgba(0,0,0,0.3)', cursor: 'pointer',
            outline: 'none', appearance: 'auto'
        };
        var modeSelect = document.createElement('select');
        Object.assign(modeSelect.style, ss);
        var oN = document.createElement('option'); oN.value = 'normal'; oN.textContent = '📋 正常';
        var oD = document.createElement('option'); oD.value = 'debug'; oD.textContent = '🐛 调试';
        modeSelect.append(oN, oD);
        var sm = localStorage.getItem(STORAGE_KEY_MODE);
        if (sm === 'debug') modeSelect.value = 'debug';
        modeSelect.addEventListener('change', function () { localStorage.setItem(STORAGE_KEY_MODE, modeSelect.value); });

        var remSelect = document.createElement('select');
        Object.assign(remSelect.style, ss);
        for (var r = 0; r < REM_OPTIONS.length; r++) {
            var oR = document.createElement('option');
            oR.value = REM_OPTIONS[r].value; oR.textContent = REM_OPTIONS[r].label;
            remSelect.appendChild(oR);
        }
        var sr = localStorage.getItem(STORAGE_KEY_REM);
        if (sr) remSelect.value = sr;
        remSelect.addEventListener('change', function () { localStorage.setItem(STORAGE_KEY_REM, remSelect.value); });

        var bindSelect = document.createElement('select');
        Object.assign(bindSelect.style, ss);
        for (var b = 0; b < BIND_OPTIONS.length; b++) {
            var oB = document.createElement('option');
            oB.value = BIND_OPTIONS[b].value; oB.textContent = BIND_OPTIONS[b].label;
            bindSelect.appendChild(oB);
        }
        var sb = localStorage.getItem(STORAGE_KEY_BIND);
        if (sb) bindSelect.value = sb;
        bindSelect.addEventListener('change', function () { localStorage.setItem(STORAGE_KEY_BIND, bindSelect.value); });

        var btn = document.createElement('div');
        Object.assign(btn.style, {
            padding: '10px 16px', backgroundColor: '#4a90d9', color: '#fff',
            fontSize: '14px', fontWeight: 'bold', borderRadius: '8px', cursor: 'pointer',
            boxShadow: '0 2px 8px rgba(0,0,0,0.3)', userSelect: 'none',
            transition: 'background-color 0.2s', whiteSpace: 'nowrap'
        });
        btn.textContent = '🔍 解析';
        btn.onmouseenter = function () { btn.style.backgroundColor = '#357abd'; };
        btn.onmouseleave = function () { btn.style.backgroundColor = '#4a90d9'; };
        btn.onclick = function () {
            var isDebug = modeSelect.value === 'debug';
            var dw = remSelect.value;
            var bf = bindSelect.value;
            btn.textContent = '⏳...'; btn.style.pointerEvents = 'none';
            requestAnimationFrame(function () {
                try { doSearch(isDebug, dw, bf); }
                catch (e) { showToast('❌ ' + e.message, true); console.error('[LayerInspector]', e); }
                finally { btn.textContent = '🔍 解析'; btn.style.pointerEvents = 'auto'; }
            });
        };
        wrapper.append(modeSelect, remSelect, bindSelect, btn);
        document.body.appendChild(wrapper);
    }

    // Node 环境导出(用于本地测试，浏览器环境自动忽略)
    if (typeof module !== 'undefined' && module.exports) {
        module.exports = { buildContainmentTree: buildContainmentTree, processNode: processNode, pickNode: pickNode, buildTree: buildTree };
    }

    if (document.body) createUI();
    else document.addEventListener('DOMContentLoaded', createUI);
})();
