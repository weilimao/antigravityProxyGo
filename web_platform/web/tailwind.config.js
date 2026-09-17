/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{vue,js,ts,jsx,tsx}",
  ],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        slate: {
          850: '#131b2e',
          950: '#070b14',
        },
        obsidian: {
          900: '#0b101e',
          950: '#06080f',
        },
        cyber: {
          cyan: '#00f2fe',
          blue: '#38bdf8',
          violet: '#8b5cf6',
          amber: '#f59e0b',
          emerald: '#10b981',
        },
      },
      boxShadow: {
        'cyber-glow-cyan': '0 0 25px -5px rgba(0, 242, 254, 0.25)',
        'cyber-glow-indigo': '0 0 30px -5px rgba(99, 102, 241, 0.3)',
        'cyber-glow-amber': '0 0 30px -5px rgba(245, 158, 11, 0.25)',
        'cyber-glow-emerald': '0 0 30px -5px rgba(16, 185, 129, 0.25)',
      },
    },
  },
  plugins: [],
}
