/** 本文件负责验证前端会员权限和套餐切换报价。 */

import assert from "node:assert/strict";
import test from "node:test";
import {
  canUseAutoReply,
  estimateSubscriptionQuote,
  normalizeSubscription,
  normalizeSubscriptionPlans,
  planPriceCents,
} from "./subscription.ts";

test("新套餐标价、有效期和实付计算一致", () => {
  const updated = normalizeSubscriptionPlans([
    { id: "monthly", member_type: "plus", duration_days: 30, original_price: 199, discount_amount: 100 },
    { id: "yearly", member_type: "pro", duration_days: 365, original_price: 2388, discount_amount: 400 },
  ]);
  assert.equal(planPriceCents(updated[0]), 9900);
  assert.equal(planPriceCents(updated[1]), 198800);
  assert.equal(updated[0].duration_days, 30);
  assert.equal(updated[1].duration_days, 365);
  const free = normalizeSubscription({ active: false, member_type: "free" });
  assert.equal(estimateSubscriptionQuote(free, updated, updated[0]).amountCents, 9900);
  assert.equal(estimateSubscriptionQuote(free, updated, updated[1]).amountCents, 198800);
});

const plans = normalizeSubscriptionPlans([
  {
    id: "monthly",
    name: "Plus包月版",
    member_type: "plus",
    duration_days: 30,
    original_price: 70,
    discount_amount: 30,
    allow_auto_reply: false,
  },
  {
    id: "yearly",
    name: "Pro包年版",
    member_type: "pro",
    duration_days: 365,
    original_price: 840,
    discount_amount: 500,
    allow_auto_reply: true,
  },
]);

test("Plus 剩余十五天升级 Pro 时抵扣二十元", () => {
  const subscription = normalizeSubscription({
    active: true,
    member_type: "plus",
    remaining_seconds: 15 * 24 * 60 * 60,
    allow_ai: true,
    allow_auto_reply: false,
  });
  const quote = estimateSubscriptionQuote(subscription, plans, plans[1]);
  assert.equal(quote.creditCents, 2000);
  assert.equal(quote.amountCents, 32000);
});

test("自动回复只接受后端返回的明确权限", () => {
  assert.equal(
    canUseAutoReply(
      normalizeSubscription({
        active: true,
        member_type: "plus",
        allow_auto_reply: false,
      }),
    ),
    false,
  );
  assert.equal(
    canUseAutoReply(
      normalizeSubscription({
        active: true,
        member_type: "pro",
        allow_auto_reply: true,
      }),
    ),
    true,
  );
});

test("有效 Pro 可以原价切换 Plus", () => {
  const subscription = normalizeSubscription({
    active: true,
    member_type: "pro",
    allow_ai: true,
    allow_auto_reply: true,
  });
  const quote = estimateSubscriptionQuote(subscription, plans, plans[0]);
  assert.equal(quote.amountCents, 4000);
  assert.equal(quote.creditCents, 0);
  assert.equal(quote.replacement, true);
  assert.equal(quote.sourceMemberType, "pro");
});
