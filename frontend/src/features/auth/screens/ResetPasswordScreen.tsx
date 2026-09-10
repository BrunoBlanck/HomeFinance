import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { ApiError } from '@/api/client'
import { Alert } from '@/components/Alert/Alert'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { Button } from '@/components/Button/Button'
import { CodeInput } from '@/components/CodeInput/CodeInput'
import { PasswordField } from '@/components/PasswordField/PasswordField'
import { TextField } from '@/components/TextField/TextField'
import { TextLink } from '@/components/TextLink/TextLink'
import { messageForError } from '@/lib/errors'
import { forgotPassword, resetPassword } from '../api/auth'
import { AuthForm } from '../components/AuthForm'
import { AuthLayout } from '../components/AuthLayout'
import { ResendCode } from '../components/ResendCode'
import { useResendCooldown } from '../hooks/useResendCooldown'
import { resetPasswordSchema } from '../schemas/auth'

/** Exatamente a mesma frase da tela de confirmação. Uma variante que
 *  diferenciasse os casos entregaria quais e-mails existem. */
const INVALID_CODE = 'Código inválido ou expirado. Peça um novo código se precisar.'

export function ResetPasswordScreen() {
  const navigate = useNavigate()
  const stateEmail = useRouterState({ select: (state) => state.location.state.email })
  const flash = useRouterState({ select: (state) => state.location.state.flash })
  const codeRef = useRef<HTMLInputElement>(null)
  const [resendDone, setResendDone] = useState(false)
  const { secondsLeft, startNextCooldown } = useResendCooldown()

  const knownEmail = stateEmail ?? ''
  const needsEmail = knownEmail === ''

  const form = useForm({
    resolver: zodResolver(resetPasswordSchema),
    defaultValues: { email: knownEmail, code: '', newPassword: '' },
    mode: 'onSubmit',
    reValidateMode: 'onBlur',
    shouldFocusError: true,
  })

  const reset = useMutation({
    mutationFn: resetPassword,
    onSuccess() {
      navigate({
        to: '/entrar',
        replace: true,
        state: {
          flash: {
            tone: 'success',
            title: 'Senha redefinida. Entre com a senha nova.',
            detail: 'Por segurança, encerramos as sessões abertas nos outros dispositivos.',
          },
        },
      })
    },
    onError() {
      codeRef.current?.focus()
      codeRef.current?.select()
    },
  })

  const resend = useMutation({
    mutationFn: forgotPassword,
    onSuccess() {
      startNextCooldown()
      setResendDone(true)
      form.setValue('code', '')
      codeRef.current?.focus()
    },
  })

  const onSubmit = form.handleSubmit((values) => {
    if (reset.isPending) return
    reset.mutate(values)
  })

  const resendError = resend.error
    ? resend.error instanceof ApiError && resend.error.status === 429
      ? 'Você pediu códigos demais. Aguarde alguns minutos antes de tentar de novo.'
      : messageForError(resend.error)
    : undefined

  return (
    <AuthLayout
      documentTitle="Criar uma senha nova"
      eyebrow="Recuperação de senha"
      step="2 de 2"
      title="Criar uma senha nova"
      intro={
        needsEmail ? (
          'Informe o e-mail que recebeu o código, digite o código e escolha a senha nova. O código vale por 15 minutos.'
        ) : (
          <>
            Digite o código enviado para <strong>{knownEmail}</strong> e escolha a senha nova. O
            código vale por 15 minutos.
          </>
        )
      }
      notice={flash ? <FlashAlert flash={flash} /> : undefined}
      footer={<TextLink to="/entrar">Voltar para entrar</TextLink>}
    >
      <AuthForm
        onSubmit={onSubmit}
        error={reset.error ? INVALID_CODE : undefined}
        focusError={false}
        primary={
          <Button type="submit" variant="primary" fullWidth loading={reset.isPending}>
            Redefinir senha
          </Button>
        }
        secondary={
          <ResendCode
            secondsLeft={secondsLeft}
            busy={resend.isPending}
            onResend={() => {
              setResendDone(false)
              resend.mutate({ email: form.getValues('email') })
            }}
          />
        }
        note={
          <>
            <p role="status">
              {resendDone ? 'Se o e-mail estiver cadastrado, enviamos um novo código.' : ''}
            </p>
            {resendError ? <Alert tone="error">{resendError}</Alert> : null}
          </>
        }
      >
        {needsEmail ? (
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
        ) : null}
        <Controller
          control={form.control}
          name="code"
          render={({ field }) => (
            <CodeInput
              ref={codeRef}
              value={field.value}
              onChange={(value) => {
                if (reset.isError) reset.reset()
                field.onChange(value)
              }}
              /* Autoenvio DESLIGADO aqui: falta a senha. Ao completar o código,
                 o foco segue para o próximo campo em vez de enviar. */
              onComplete={() => form.setFocus('newPassword')}
              busy={reset.isPending}
              autoFocus={!needsEmail}
              invalid={reset.isError}
              error={form.formState.errors.code?.message}
            />
          )}
        />
        <PasswordField
          {...form.register('newPassword')}
          label="Nova senha"
          autoComplete="new-password"
          showRequirement
          hint="Prefira uma frase que só você saiba."
          error={form.formState.errors.newPassword?.message}
        />
      </AuthForm>
    </AuthLayout>
  )
}
