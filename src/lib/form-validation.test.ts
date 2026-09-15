import { afterEach, beforeEach, expect, it } from 'vitest'
import { installChineseValidation } from './form-validation'

let cleanup: () => void
beforeEach(() => { cleanup = installChineseValidation(document) })
afterEach(() => { cleanup(); document.body.innerHTML = '' })

it('localizes required fields and clears the error after correction', () => {
  document.body.innerHTML = '<input required><textarea required></textarea><select required><option value="">请选择</option><option value="a">A</option></select>'
  for (const field of document.querySelectorAll<HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement>('input, textarea, select')) {
    expect(field.checkValidity()).toBe(false)
    expect(field.validationMessage).toBe(field instanceof HTMLSelectElement ? '请选择一项' : '请填写此字段')
    field.value = 'a'
    field.dispatchEvent(new Event('input', { bubbles: true }))
    expect(field.checkValidity()).toBe(true)
  }
})

it('updates number constraints and clears stale errors on revalidation', () => {
  const field = document.createElement('input')
  field.type = 'number'; field.min = '1'; field.max = '10'; field.value = '0'
  document.body.append(field)
  field.checkValidity()
  expect(field.validationMessage).toBe('请输入不小于 1 的值')
  field.value = '11'
  field.dispatchEvent(new Event('change', { bubbles: true }))
  expect(field.validationMessage).toBe('请输入不大于 10 的值')
  field.value = '5'
  field.checkValidity()
  expect(field.validationMessage).toBe('')
  expect(field.checkValidity()).toBe(true)
})

it('localizes URLs without overwriting application custom errors', () => {
  const field = document.createElement('input')
  field.type = 'url'; field.value = 'invalid'
  document.body.append(field)
  field.checkValidity()
  expect(field.validationMessage).toBe('请输入有效的网址')
  field.setCustomValidity('此代理不可用')
  field.dispatchEvent(new Event('input', { bubbles: true }))
  expect(field.validationMessage).toBe('此代理不可用')
})
