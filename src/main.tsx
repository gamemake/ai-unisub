import { createRoot } from 'react-dom/client'
import { QueryClientProvider } from '@tanstack/react-query'
import { queryClient } from './data/client'
import App from './app'
import './index.css'
import { installChineseValidation } from './lib/form-validation'

installChineseValidation(document)

createRoot(document.getElementById('root')!).render(<QueryClientProvider client={queryClient}><App /></QueryClientProvider>)
