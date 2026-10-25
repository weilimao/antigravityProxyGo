import test from 'node:test';
import assert from 'node:assert/strict';

// 与 dashboardBenchmark.ts 完全对齐的纯函数逻辑测试
function parseBenchmarkStatusCode(r) {
    if (r && typeof r.statusCode === 'number' && r.statusCode > 0) {
        return { code: r.statusCode, label: `${r.statusCode}`, text: `HTTP ${r.statusCode}` };
    }
    const err = String(r?.error || '');
    if (err) {
        const m = err.match(/(?:HTTP\s*|code\s*[:=]\s*)(\d{3})/i) || err.match(/\b([45]\d{2})\b/);
        if (m) {
            const num = parseInt(m[1], 10);
            return { code: num, label: `${num}`, text: `HTTP ${num}` };
        }
        if (/timeout|deadline exceeded/i.test(err)) {
            return { code: 504, label: '504', text: '504 (超时)' };
        }
    }
    return { code: 0, label: '', text: '' };
}

function computeMetricDisplay(r, isZh = true) {
    const st = r?.status || 'ok';
    const statusInfo = parseBenchmarkStatusCode(r);
    const colLabel = st === 'error' ? (isZh ? '状态' : 'Status') : (isZh ? '首帧' : 'TTFT');
    let ttftShown = '';
    let totalShown = '';
    let badgeLabel = '';

    if (st === 'pending') {
        ttftShown = isZh ? '测速中...' : 'running...';
        totalShown = '--';
    } else if (st === 'error') {
        badgeLabel = statusInfo.label || 'ERR';
        if (statusInfo.text) {
            ttftShown = `${statusInfo.text} (${isZh ? '失败' : 'fail'})`;
        } else {
            ttftShown = isZh ? '失败' : 'fail';
        }
        totalShown = '-';
    } else {
        ttftShown = `${r.ttftMs}ms`;
        totalShown = `${r.totalMs}ms`;
    }

    return { colLabel, ttftShown, totalShown, badgeLabel, statusInfo };
}

test('Case 1: 后端显式返回 statusCode=500 时的解析', () => {
    const res = parseBenchmarkStatusCode({
        model: 'test-model',
        status: 'error',
        statusCode: 500,
        error: 'HTTP 500: Internal Server Error'
    });
    assert.equal(res.code, 500);
    assert.equal(res.label, '500');
    assert.equal(res.text, 'HTTP 500');
});

test('Case 2: 存量旧记录无 statusCode 字段，从 error 正则回退提取 404', () => {
    const res = parseBenchmarkStatusCode({
        model: 'nvidia/deepseek',
        status: 'error',
        error: 'HTTP 404: {"error":{"message":"model not found"}}'
    });
    assert.equal(res.code, 404);
    assert.equal(res.label, '404');
    assert.equal(res.text, 'HTTP 404');
});

test('Case 3: 429 配额用尽 / 限流错误提取', () => {
    const res = parseBenchmarkStatusCode({
        model: 'workbuddy/ds',
        status: 'error',
        statusCode: 429,
        error: 'HTTP 429: Active accounts quota exhausted'
    });
    assert.equal(res.code, 429);
    assert.equal(res.label, '429');
    assert.equal(res.text, 'HTTP 429');
});

test('Case 4: 请求超时 (context deadline exceeded) 映射 504', () => {
    const res = parseBenchmarkStatusCode({
        model: 'slow-model',
        status: 'error',
        error: 'Post "http://127.0.0.1:18444/route": context deadline exceeded'
    });
    assert.equal(res.code, 504);
    assert.equal(res.label, '504');
    assert.equal(res.text, '504 (超时)');
});

test('Case 5: 成功状态 200 正常输出', () => {
    const res = parseBenchmarkStatusCode({
        model: 'fast-model',
        status: 'ok',
        statusCode: 200,
        ttftMs: 311,
        totalMs: 240
    });
    assert.equal(res.code, 200);
    assert.equal(res.label, '200');
    assert.equal(res.text, 'HTTP 200');
});

test('Case 6: 卡片第二行指标与 Badge 渲染计算 (中文环境 - 错误态)', () => {
    const metric = computeMetricDisplay({
        model: 'minimax',
        status: 'error',
        statusCode: 502,
        error: 'HTTP 502: Bad Gateway'
    }, true);
    assert.equal(metric.colLabel, '状态');
    assert.equal(metric.ttftShown, 'HTTP 502 (失败)');
    assert.equal(metric.totalShown, '-');
    assert.equal(metric.badgeLabel, '502');
});

test('Case 7: 卡片第二行指标与 Badge 渲染计算 (英文环境 - 错误态)', () => {
    const metric = computeMetricDisplay({
        model: 'minimax',
        status: 'error',
        statusCode: 401,
        error: 'HTTP 401: Unauthorized'
    }, false);
    assert.equal(metric.colLabel, 'Status');
    assert.equal(metric.ttftShown, 'HTTP 401 (fail)');
    assert.equal(metric.totalShown, '-');
    assert.equal(metric.badgeLabel, '401');
});

test('Case 8: 成功态正常渲染首帧与耗时 (不展示错误 Badge)', () => {
    const metric = computeMetricDisplay({
        model: 'minimax',
        status: 'ok',
        statusCode: 200,
        ttftMs: 598,
        totalMs: 70
    }, true);
    assert.equal(metric.colLabel, '首帧');
    assert.equal(metric.ttftShown, '598ms');
    assert.equal(metric.totalShown, '70ms');
    assert.equal(metric.badgeLabel, '');
});
