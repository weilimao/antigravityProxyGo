export interface HSVA {
  h: number; // 0 ~ 360
  s: number; // 0 ~ 100
  v: number; // 0 ~ 100
  a: number; // 0 ~ 1
}

export interface RGBA {
  r: number; // 0 ~ 255
  g: number; // 0 ~ 255
  b: number; // 0 ~ 255
  a: number; // 0 ~ 1
}

/**
 * 将 RGBA 转换为 HSVA
 */
export function rgbaToHsva(r: number, g: number, b: number, a = 1): HSVA {
  const rNorm = r / 255;
  const gNorm = g / 255;
  const bNorm = b / 255;

  const max = Math.max(rNorm, gNorm, bNorm);
  const min = Math.min(rNorm, gNorm, bNorm);
  const delta = max - min;

  let h = 0;
  if (delta !== 0) {
    if (max === rNorm) {
      h = ((gNorm - bNorm) / delta) % 6;
    } else if (max === gNorm) {
      h = (bNorm - rNorm) / delta + 2;
    } else {
      h = (rNorm - gNorm) / delta + 4;
    }
    h = Math.round(h * 60);
    if (h < 0) h += 360;
  }

  const s = max === 0 ? 0 : Math.round((delta / max) * 100);
  const v = Math.round(max * 100);

  return { h, s, v, a: Math.min(1, Math.max(0, Math.round(a * 100) / 100)) };
}

/**
 * 将 HSVA 转换为 RGBA
 */
export function hsvaToRgba(h: number, s: number, v: number, a = 1): RGBA {
  const hNorm = (h % 360) / 60;
  const sNorm = s / 100;
  const vNorm = v / 100;

  const c = vNorm * sNorm;
  const x = c * (1 - Math.abs((hNorm % 2) - 1));
  const m = vNorm - c;

  let r = 0;
  let g = 0;
  let b = 0;

  if (hNorm >= 0 && hNorm < 1) {
    r = c; g = x; b = 0;
  } else if (hNorm >= 1 && hNorm < 2) {
    r = x; g = c; b = 0;
  } else if (hNorm >= 2 && hNorm < 3) {
    r = 0; g = c; b = x;
  } else if (hNorm >= 3 && hNorm < 4) {
    r = 0; g = x; b = c;
  } else if (hNorm >= 4 && hNorm < 5) {
    r = x; g = 0; b = c;
  } else if (hNorm >= 5 && hNorm < 6) {
    r = c; g = 0; b = x;
  }

  return {
    r: Math.round((r + m) * 255),
    g: Math.round((g + m) * 255),
    b: Math.round((b + m) * 255),
    a: Math.min(1, Math.max(0, Math.round(a * 100) / 100))
  };
}

/**
 * 将 HSVA 转为 HEX 或 8位带有 Alpha 的 HEX 字符串 (如 #6366f1 或 #6366f133)
 */
export function hsvaToHexString(h: number, s: number, v: number, a = 1, includeAlpha = false): string {
  const { r, g, b } = hsvaToRgba(h, s, v, a);
  const rHex = r.toString(16).padStart(2, '0');
  const gHex = g.toString(16).padStart(2, '0');
  const bHex = b.toString(16).padStart(2, '0');

  if (includeAlpha || a < 0.999) {
    const aHex = Math.round(a * 255).toString(16).padStart(2, '0');
    return `#${rHex}${gHex}${bHex}${aHex}`;
  }
  return `#${rHex}${gHex}${bHex}`;
}

/**
 * 将 HSVA 转为标准 CSS rgba(...) 字符串
 */
export function hsvaToRgbaString(h: number, s: number, v: number, a = 1): string {
  const { r, g, b } = hsvaToRgba(h, s, v, a);
  const alphaFormatted = Number(a.toFixed(2));
  return `rgba(${r}, ${g}, ${b}, ${alphaFormatted})`;
}

/**
 * 智能解析任意格式的颜色字符串（支持 #fff, #ffffff, #ffffff80, rgb(r,g,b), rgba(r,g,b,a)）
 */
export function parseColorToHsva(colorStr: string): HSVA {
  if (!colorStr) return { h: 240, s: 60, v: 80, a: 1 };
  const str = colorStr.trim().toLowerCase();

  // 1. 解析 rgba(...) 或 rgb(...)
  const rgbaMatch = str.match(/rgba?\s*\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)(?:\s*,\s*([\d.]+))?\s*\)/);
  if (rgbaMatch) {
    const r = parseInt(rgbaMatch[1], 10) || 0;
    const g = parseInt(rgbaMatch[2], 10) || 0;
    const b = parseInt(rgbaMatch[3], 10) || 0;
    const a = rgbaMatch[4] !== undefined ? parseFloat(rgbaMatch[4]) : 1;
    return rgbaToHsva(r, g, b, a);
  }

  // 2. 解析 HEX (#fff, #ffffff, #ffffffff)
  if (str.startsWith('#')) {
    let hex = str.slice(1);
    if (hex.length === 3 || hex.length === 4) {
      hex = hex.split('').map(c => c + c).join('');
    }
    if (hex.length === 6) {
      const r = parseInt(hex.slice(0, 2), 16) || 0;
      const g = parseInt(hex.slice(2, 4), 16) || 0;
      const b = parseInt(hex.slice(4, 6), 16) || 0;
      return rgbaToHsva(r, g, b, 1);
    }
    if (hex.length === 8) {
      const r = parseInt(hex.slice(0, 2), 16) || 0;
      const g = parseInt(hex.slice(2, 4), 16) || 0;
      const b = parseInt(hex.slice(4, 6), 16) || 0;
      const a = (parseInt(hex.slice(6, 8), 16) || 0) / 255;
      return rgbaToHsva(r, g, b, a);
    }
  }

  // 兜底白色
  return { h: 0, s: 0, v: 100, a: 1 };
}
