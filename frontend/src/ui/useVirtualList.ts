import { ref, computed, watch, onMounted, onScopeDispose, getCurrentScope, getCurrentInstance, type Ref, type ComputedRef } from 'vue';

export interface VirtualWindowResult {
  startIndex: number;
  endIndex: number;
  topSpacerHeight: number;
  bottomSpacerHeight: number;
  totalHeight: number;
}

/**
 * 纯数学切片计算函数: 供 Composable 响应式调度与独立单元测试复用
 * 具备自动边界保护: 当 scrollTop 超过最大可滚动高度时自愈夹回，杜绝搜索筛选后出现切片越界或白屏
 */
export function calculateVirtualWindow(
  totalCount: number,
  scrollTop: number,
  viewportHeight: number,
  itemHeight: number,
  buffer: number = 5
): VirtualWindowResult {
  const totalHeight = totalCount * itemHeight;
  if (totalCount <= 0) {
    return { startIndex: 0, endIndex: 0, topSpacerHeight: 0, bottomSpacerHeight: 0, totalHeight: 0 };
  }
  const safeViewport = Math.max(1, viewportHeight);
  const maxScroll = Math.max(0, totalHeight - safeViewport);
  const safeScrollTop = Math.max(0, Math.min(scrollTop, maxScroll));

  const rawStart = Math.floor(safeScrollTop / itemHeight);
  const rawEnd = Math.ceil((safeScrollTop + safeViewport) / itemHeight);

  const startIndex = Math.max(0, rawStart - buffer);
  const endIndex = Math.min(totalCount, rawEnd + buffer);

  const topSpacerHeight = startIndex * itemHeight;
  const bottomSpacerHeight = Math.max(0, (totalCount - endIndex) * itemHeight);

  return {
    startIndex,
    endIndex,
    topSpacerHeight,
    bottomSpacerHeight,
    totalHeight,
  };
}

export interface UseVirtualListOptions<T> {
  items: Ref<T[]> | ComputedRef<T[]>;
  itemHeight: number;
  buffer?: number;
  defaultViewportHeight?: number;
}

export function useVirtualList<T>(options: UseVirtualListOptions<T>) {
  const {
    items,
    itemHeight,
    buffer = 5,
    defaultViewportHeight = 420,
  } = options;

  const containerRef = ref<HTMLElement | null>(null);
  const scrollTop = ref(0);
  const viewportHeight = ref(defaultViewportHeight);

  const totalCount = computed(() => items.value.length);

  const windowResult = computed<VirtualWindowResult>(() => {
    return calculateVirtualWindow(
      totalCount.value,
      scrollTop.value,
      viewportHeight.value,
      itemHeight,
      buffer
    );
  });

  const startIndex = computed(() => windowResult.value.startIndex);
  const endIndex = computed(() => windowResult.value.endIndex);
  const topSpacerHeight = computed(() => windowResult.value.topSpacerHeight);
  const bottomSpacerHeight = computed(() => windowResult.value.bottomSpacerHeight);
  const totalHeight = computed(() => windowResult.value.totalHeight);

  const visibleItems = computed<T[]>(() => {
    if (totalCount.value === 0) return [];
    return items.value.slice(startIndex.value, endIndex.value);
  });

  function handleScroll(e?: Event) {
    if (containerRef.value) {
      scrollTop.value = containerRef.value.scrollTop;
    } else if (e && e.target) {
      scrollTop.value = (e.target as HTMLElement).scrollTop;
    }
  }

  function scrollToIndex(index: number, smooth: boolean = false) {
    if (!containerRef.value) return;
    const targetTop = Math.max(0, Math.min(index * itemHeight, totalHeight.value - viewportHeight.value));
    if (smooth && typeof containerRef.value.scrollTo === 'function') {
      containerRef.value.scrollTo({ top: targetTop, behavior: 'smooth' });
    } else {
      containerRef.value.scrollTop = targetTop;
    }
    scrollTop.value = targetTop;
  }

  function scrollToTop(smooth: boolean = false) {
    scrollToIndex(0, smooth);
  }

  function scrollToBottom(smooth: boolean = false) {
    scrollToIndex(totalCount.value, smooth);
  }

  function resetScroll() {
    if (containerRef.value) {
      containerRef.value.scrollTop = 0;
    }
    scrollTop.value = 0;
  }

  // 监听容器大小变更, 自适应更新视口高度
  let resizeObserver: ResizeObserver | null = null;

  function updateViewport() {
    if (containerRef.value && containerRef.value.clientHeight > 0) {
      viewportHeight.value = containerRef.value.clientHeight;
    }
  }

  if (getCurrentInstance()) {
    onMounted(() => {
      updateViewport();
      if (typeof ResizeObserver !== 'undefined' && containerRef.value) {
        resizeObserver = new ResizeObserver(() => {
          updateViewport();
        });
        resizeObserver.observe(containerRef.value);
      }
    });
  }

  if (getCurrentScope()) {
    onScopeDispose(() => {
      if (resizeObserver) {
        resizeObserver.disconnect();
        resizeObserver = null;
      }
    });
  }

  // 当 items 总数缩减时 (例如搜索筛选过滤), 若滚动位置超出边界则夹回
  watch(totalCount, (newCount) => {
    const maxScroll = Math.max(0, newCount * itemHeight - viewportHeight.value);
    if (scrollTop.value > maxScroll) {
      if (containerRef.value) {
        containerRef.value.scrollTop = maxScroll;
      }
      scrollTop.value = maxScroll;
    }
  });

  return {
    containerRef,
    scrollTop,
    viewportHeight,
    totalHeight,
    startIndex,
    endIndex,
    topSpacerHeight,
    bottomSpacerHeight,
    visibleItems,
    handleScroll,
    scrollToIndex,
    scrollToTop,
    scrollToBottom,
    resetScroll,
  };
}
