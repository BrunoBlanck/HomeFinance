import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { useNavigate } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { Button } from '@/components/Button/Button'
import { TextField } from '@/components/TextField/TextField'
import { TextLink } from '@/components/TextLink/TextLink'
import { messageForError } from '@/lib/errors'
import { forgotPassword } from '../api/auth'
import { AuthForm } from '../components/AuthForm'
import { AuthLayout } from '../components/AuthLayout'
import { clearResendCooldown } from '../hooks/useResendCooldown'
import { forgotPasswordSchema } from '../schemas/auth'

export function ForgotPasswordScreen() {
  const navigate = useNavigate()

  const form = useForm({
    resolver: zodResolver(forgotPasswordSchema),
    defaultValues: { email: '' },
    mode: 'onSubmit',
    reValidateMode: 'onBlur',
    shouldFocusError: true,
  })

  const mutation = useMutation({
    mutationFn: forgotPassword,
    onSuccess(_result, variables) {
      clearResendCooldown()
      navigate({
        to: '/redefinir-senha',
        state: {
          email: variables.email,
          flash: {
            tone: 'info',
            message: `Se houver uma conta com ${variables.email}, o código chega em instantes. Confira também a caixa de spam.`,
          },
        },
      })
    },
  })

  const onSubmit = form.handleSubmit((values) => {
    if (mutation.isPending) return
    mutation.mutate(values)
  })

  return (
    <AuthLayout
      documentTitle="Esqueci minha senha"
      eyebrow="Recuperação de senha"
      step="1 de 2"
      title="Esqueci minha senha"
      /* A condição vem ANTES do envio, como expectativa. Dita depois, a mesma
         frase soaria desculpa — e é o que faz a resposta uniforme não parecer evasiva. */
      intro="Informe seu e-mail. Se houver uma conta com ele, enviamos um código de 6 dígitos para você criar uma senha nova."
      footer={
        <p>
          Lembrou a senha? <TextLink to="/entrar">Entrar</TextLink>
        </p>
      }
    >
      <AuthForm
        onSubmit={onSubmit}
        error={mutation.error ? messageForError(mutation.error) : undefined}
        primary={
          <Button type="submit" variant="primary" fullWidth loading={mutation.isPending}>
            Enviar código
          </Button>
        }
      >
        <TextField
          {...form.register('email')}
          label="E-mail"
          type="email"
          autoComplete="email"
          inputMode="email"
          autoCapitalize="off"
          spellCheck={false}
          error={form.formState.errors.email?.message}
        />
      </AuthForm>
    </AuthLayout>
  )
}
