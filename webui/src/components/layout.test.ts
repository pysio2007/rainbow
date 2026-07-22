import { describe, expect, it } from 'vitest'
import { headerContainerClassName, headerNavClassName } from './layout'

describe('Header responsive layout contract', () => {
  it('keeps the mobile header within its content width and allows navigation to wrap', () => {
    expect(headerContainerClassName).toContain('flex-col')
    expect(headerContainerClassName).toContain('sm:flex-row')
    expect(headerNavClassName).toContain('w-full')
    expect(headerNavClassName).toContain('flex-wrap')
    expect(headerNavClassName).toContain('sm:w-auto')
  })
})
