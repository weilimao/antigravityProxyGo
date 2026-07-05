// Mock the window object for Node.js environment before importing ipc.ts
const mockWindow: any = {
    wailsPendingListeners: []
};

// Mock EventsOnMultiple handler
const mockEventsOnMultiple = (eventName: string, callback: any, maxCallbacks: number) => {
    mockWindow.lastRegisteredEvent = eventName;
    mockWindow.lastRegisteredCallback = callback;
    return () => {};
};

// Expose mockWindow globally
(global as any).window = mockWindow;

// Mock window.go functions required during imports
mockWindow.go = {
    main: {
        App: {
            IPCSend: async () => {},
            IPCInvoke: async () => JSON.stringify({}),
            OpenPath: async () => {},
            ShowItemInFolder: async () => {}
        }
    }
};

// Import the module under test dynamically inside runTests to prevent ES import hoisting

async function runTests() {
    const { ipcRenderer } = await import('./ipc');
    console.log("=== 开始运行 ipc.ts 单元测试 ===");

    // Test Case 1: Wails 未就绪时，ipcRenderer.on 应该将回调缓存到 window.wailsPendingListeners
    console.log("测试 1: 验证 Wails 未就绪时监听器的 pending 缓存机制...");
    delete mockWindow.runtime; // 确保 window.runtime 是 undefined
    mockWindow.wailsPendingListeners = [];

    let test1Called = false;
    ipcRenderer.on('test-channel-1', (event, ...args) => {
        test1Called = true;
    });

    if (mockWindow.wailsPendingListeners.length !== 1) {
        throw new Error(`测试 1 失败: wailsPendingListeners 长度应为 1，实际为 ${mockWindow.wailsPendingListeners.length}`);
    }
    if (mockWindow.wailsPendingListeners[0].channel !== 'test-channel-1') {
        throw new Error(`测试 1 失败: 缓存的 channel 应该是 'test-channel-1'`);
    }
    console.log("✓ 测试 1 通过: 监听器已成功缓存到 wailsPendingListeners。");

    // Test Case 2: Wails 就绪时，ipcRenderer.on 应该直接调用 wailsRuntime.EventsOn 绑定
    console.log("测试 2: 验证 Wails 已就绪时监听器的直接绑定机制...");
    mockWindow.runtime = {
        EventsOnMultiple: mockEventsOnMultiple
    };

    let test2Called = false;
    ipcRenderer.on('test-channel-2', (event, ...args) => {
        test2Called = true;
    });

    if (mockWindow.lastRegisteredEvent !== 'test-channel-2') {
        throw new Error(`测试 2 失败: 未能正确通过 Wails Runtime 绑定 'test-channel-2'`);
    }
    console.log("✓ 测试 2 通过: 监听器已直接通过 Wails Runtime 绑定。");

    // Test Case 3: 当 initWailsReady() 被触发时，pending 队列中的所有监听器应该被 flush 出来并正常注册
    console.log("测试 3: 验证 initWailsReady 被调用时，pending 监听器是否被正确 flush 绑定...");
    // 重置状态：Wails 未就绪时注册一个监听器
    delete mockWindow.runtime;
    mockWindow.wailsPendingListeners = [];
    
    let test3Called = false;
    ipcRenderer.on('test-channel-3', (event, ...args) => {
        test3Called = true;
    });

    // 此时 it 应该在 pending 里
    if (mockWindow.wailsPendingListeners.length !== 1) {
        throw new Error(`测试 3 准备失败: 监听器未进入 pending`);
    }

    // 模拟 Wails 此时已就绪
    mockWindow.runtime = {
        EventsOnMultiple: mockEventsOnMultiple
    };

    // 模拟 Wails 触发 initWailsReady()
    if (typeof mockWindow.initWailsReady !== 'function') {
        throw new Error(`测试 3 失败: initWailsReady 未在 window 上正确挂载`);
    }

    mockWindow.initWailsReady();

    // 验证 pending 是否被清空
    if (mockWindow.wailsPendingListeners && mockWindow.wailsPendingListeners.length !== 0) {
        throw new Error(`测试 3 失败: initWailsReady 调用后 pending 队列未清空，仍有 ${mockWindow.wailsPendingListeners.length} 个项目`);
    }

    // 验证最终是否成功通过 EventsOnMultiple 绑定
    if (mockWindow.lastRegisteredEvent !== 'test-channel-3') {
        throw new Error(`测试 3 失败: initWailsReady 执行后，'test-channel-3' 监听器未被绑定 to Wails 运行时`);
    }
    console.log("✓ 测试 3 通过: initWailsReady 成功 flush 且绑定了 pending 监听器。");

    console.log("\n>>> 所有测试用例全部绿灯通过！ <<<");
}

runTests().catch((err) => {
    console.error("❌ 测试执行失败:", err);
    process.exit(1);
});
