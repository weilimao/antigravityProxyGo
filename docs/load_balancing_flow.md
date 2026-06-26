# 负载均衡重试流程分析 (同步配额查询)

本文档展示了在发生 `CAPACITY_EXHAUSTED` (HTTP 503) 错误时，系统采用的**同步配额查询**与重试流程。

---

## 1. 重试控制流

在修改后的设计中，配额的获取是同步进行的，从而确保了重试触发前冷静期状态的绝对确定性。

```mermaid
flowchart TD
    Start([开始请求]) --> Route1[1. 粘性路由分配账号<br/>选中 weilimao96@gmail.com]
    Route1 --> Req1[2. 执行请求 (第 1 次尝试)]
    Req1 --> Err1{3. 收到 HTTP 503<br/>MODEL_CAPACITY_EXHAUSTED?}
    
    Err1 -- Yes --> SetCool[4. 设置 5分钟 冷静期]
    SetCool --> QuotaFetch[5. 同步执行 quotaFetch<br/>等待接口返回]
    
    QuotaFetch --> QuotaCheck{6. 配额是否真的耗尽?}
    
    %% 清除冷静期
    QuotaCheck -- No --> ResetCool[7. 同步清除该账号的冷静期<br/>恢复为可用状态]
    
    %% 不清除冷静期
    QuotaCheck -- Yes --> KeepCool[8. 保留 5分钟 冷静期]
    
    ResetCool --> WaitRetry[9. 等待退避重试延迟]
    KeepCool --> WaitRetry
    
    WaitRetry --> Route2[10. 重新分配账号重试<br/>GetOrAssignAccount]
    Route2 --> CheckAvail{11. 原账号是否在可用列表中?}
    
    %% 如果配额未耗尽（已恢复），则原地重试
    CheckAvail -- Yes (原账号可用) --> ReuseAcc[12. 粘性路由重新选中原账号<br/>weilimao96@gmail.com]
    ReuseAcc --> Req2[13. 执行重试 (第 2 次尝试)]
    
    %% 如果配额已耗尽，则自动切换账号
    CheckAvail -- No (原账号不可用) --> SwitchAcc[12. 粘性路由分配其它可用账号]
    SwitchAcc --> Req2Alt[13. 在新账号上执行重试]
```

---

## 2. 关键代码入口

* 503 错误捕获及同步配额查询：`internal/proxy/handler.go` 中的 `errAttempt.Error() == "CAPACITY_EXHAUSTED"` 分支
* 配额更新与冷静期计算：`internal/account/account.go` 中的 `UpdateAccountCooldownFromQuota` 函数
* 粘性路由选择：`internal/session/session.go` 中的 `GetOrAssignAccount` 函数

---

## 3. 设计优势

1. **消除了竞态条件**：重试线程不再与后台配额查询进行时间竞速。配额查询的成功与否必定在下一次尝试开始前确定。
2. **保证行为确定性**：
   * 如果配额真的充足，代理可以百分之百安全地在原账号上原地重试，而不会非预期地切换至其他账号。
   * 避免了由于配额拉取接口慢响应（超过 10 秒）导致的误判与误切换账号行为。
