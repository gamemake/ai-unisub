type Control = HTMLInputElement | HTMLTextAreaElement | HTMLSelectElement

export function installChineseValidation(root: Document) {
  const messages = new WeakMap<Control, string>()
  function localize(event: Event) {
    const field = event.target
    if (!(field instanceof HTMLInputElement || field instanceof HTMLTextAreaElement || field instanceof HTMLSelectElement)) return
    if (messages.get(field) === field.validationMessage) field.setCustomValidity('')
    messages.delete(field)
    const validity = field.validity
    if (validity.valid || validity.customError) return
    let message = '请输入有效的内容'
    if (validity.valueMissing) message = field instanceof HTMLSelectElement ? '请选择一项' : '请填写此字段'
    else if (validity.badInput) message = '请输入有效的数字'
    else if (validity.typeMismatch) message = field instanceof HTMLInputElement && field.type === 'email' ? '请输入有效的邮箱地址' : '请输入有效的网址'
    else if (validity.tooShort) message = `请至少输入 ${(field as HTMLInputElement).minLength} 个字符`
    else if (validity.tooLong) message = `请最多输入 ${(field as HTMLInputElement).maxLength} 个字符`
    else if (validity.rangeUnderflow) message = `请输入不小于 ${(field as HTMLInputElement).min} 的值`
    else if (validity.rangeOverflow) message = `请输入不大于 ${(field as HTMLInputElement).max} 的值`
    else if (validity.stepMismatch) message = `请输入符合步长 ${(field as HTMLInputElement).step || '1'} 的值`
    else if (validity.patternMismatch) message = '请按要求的格式填写'
    field.setCustomValidity(message)
    messages.set(field, message)
  }
  // Capture also covers portaled dialogs and the Select component's native control.
  root.addEventListener('invalid', localize, true)
  root.addEventListener('input', localize, true)
  root.addEventListener('change', localize, true)
  return () => {
    root.removeEventListener('invalid', localize, true)
    root.removeEventListener('input', localize, true)
    root.removeEventListener('change', localize, true)
  }
}
