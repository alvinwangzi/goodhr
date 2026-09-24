/** 本文件验证简历进度的展示数据，避免把待索要或已收到误显示为已下载。 */
import assert from 'node:assert/strict';
import { test } from 'node:test';
import { normalizeCandidate, candidateEventLabel } from './candidate-normalize.ts';

for (const [state, text] of [['pending', '待索要'], ['requested', '已索要'], ['received', '已收到'], ['downloaded', '已下载']]) {
  test(`简历进度：${text}独立于结构化正文`, () => {
    const item = normalizeCandidate({ id: 'one', resume_state: state, resume_error: '', resume_updated_at: '2026-09-24T01:00:00Z' });
    assert.equal(item.resumeState, state);
    assert.equal(item.resumeStateText, text);
    assert.equal(item.rawText, '');
  });
}
test('下载失败保留已收到进度和失败原因', () => {
  const item = normalizeCandidate({ resume_state: 'received', resume_error: '下载超时' });
  assert.equal(item.resumeState, 'received');
  assert.equal(item.resumeError, '下载超时');
  assert.equal(candidateEventLabel('resume_tracking_failed'), '简历操作未完成');
});
test('历史索要时间兼容显示，空档案不编造已收到或已下载', () => {
  assert.equal(normalizeCandidate({ resume_requested_at: '2026-09-23T00:00:00Z' }).resumeState, 'requested');
  assert.equal(normalizeCandidate({}).resumeState, '');
});
