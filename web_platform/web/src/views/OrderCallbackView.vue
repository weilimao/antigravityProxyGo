<template>
  <div class="min-h-screen flex items-center justify-center p-4">
    <div class="glass-card-glow w-full max-w-md p-8 text-center">
      <div class="w-16 h-16 rounded-full bg-emerald-500/20 text-emerald-400 mx-auto flex items-center justify-center mb-4 border border-emerald-500/30">
        <span class="material-symbols-outlined text-36px">verified</span>
      </div>
      <h2 class="text-xl font-bold text-white mb-2">订阅状态确认</h2>
      <p class="text-xs text-slate-400 mb-6">
        若您已完成扫码支付，平台系统将在秒级内自动为您激活并顺延套餐时长。
      </p>

      <div class="flex items-center justify-center gap-3">
        <router-link to="/dashboard" class="btn-primary text-xs">
          <span class="material-symbols-outlined text-16px">dashboard</span>
          <span>进入个人控制台</span>
        </router-link>
        <router-link to="/pricing" class="btn-secondary text-xs">
          <span>返回套餐列表</span>
        </router-link>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
// 极客工坊收银完成着陆页
</script>
