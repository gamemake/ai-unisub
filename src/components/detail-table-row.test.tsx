import { afterEach, expect, it, vi } from 'vitest'
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { DetailTableRow } from './detail-table-row'

afterEach(cleanup)

it('opens details with row clicks and keyboard activation', async () => {
  const onOpen = vi.fn(), user = userEvent.setup()
  render(<table><tbody><DetailTableRow onOpen={onOpen}><td>Provider</td></DetailTableRow></tbody></table>)
  await user.click(screen.getByText('Provider'))
  screen.getByRole('row').focus()
  await user.keyboard('{Enter} ')
  expect(onOpen).toHaveBeenCalledTimes(3)
})

it('does not open details when using nested controls', async () => {
  const onOpen = vi.fn(), onDelete = vi.fn(), user = userEvent.setup()
  render(<table><tbody><DetailTableRow onOpen={onOpen}><td><button onClick={onDelete}>Delete</button><label><input type="checkbox" />Enabled</label><input aria-label="Name" /></td></DetailTableRow></tbody></table>)
  await user.click(screen.getByRole('button'))
  await user.keyboard('{Enter} ')
  await user.click(screen.getByLabelText('Enabled'))
  await user.type(screen.getByLabelText('Name'), 'a b{Enter}')
  expect(onDelete).toHaveBeenCalledTimes(3)
  expect(onOpen).not.toHaveBeenCalled()
})

it('keeps rows without details non-interactive', () => {
  render(<table><tbody><DetailTableRow><td>Archived</td></DetailTableRow></tbody></table>)
  expect(screen.getByRole('row').hasAttribute('tabindex')).toBe(false)
  expect(screen.getByRole('row').hasAttribute('aria-haspopup')).toBe(false)
})
