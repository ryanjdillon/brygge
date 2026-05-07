// Vite turns these imports into URL strings at build time. Declared
// here so vue-tsc resolves `import logo from '@/assets/foo.svg'` —
// the project doesn't pull in `vite/client`, so we name the modules
// we actually use rather than reference the whole client typings.

declare module '*.svg' {
  const src: string
  export default src
}

declare module '*.png' {
  const src: string
  export default src
}

declare module '*.jpg' {
  const src: string
  export default src
}
