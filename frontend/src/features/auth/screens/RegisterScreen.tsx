import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation } from '@tanstack/react-query'
import { useNavigate, useRouterState } from '@tanstack/react-router'
import { useForm } from 'react-hook-form'
import { FlashAlert } from '@/components/Alert/FlashAlert'
import { Button } from '@/components/Button/Button'
import { PasswordField } from '@/components/PasswordField/PasswordField'
import { TextField } from '@/components/TextField/TextField'
import { TextLink } from '@/components/TextLink/TextLink'
import { messageForError } from '@/lib/errors'
import { register as registerAccount } from '../api/auth'
import { AuthForm } from '../components/AuthForm'
import { AuthLayout } from '../components/AuthLayout'
import { clearResendCooldown } from '../hooks/useResendCooldown'
import { registerSchema } from '../schemas/auth'
import { saveRegistration } from '../storage/registrationToken'

export function RegisterScreen() {
  const navigate = useNavigate()
  // Quem cai aqui vindo do login sem token utilizável traz o e-mail no state do
  // roteador, para não precisar digitar de novo. State, nunca query string.
  const stateEmail = useRouterState({ select: (state) => state.location.state.email })
  const flash = useRouterState({ select: (state) => state.location.state.flash })

  const form = useForm({
    resolver: zodResolver(registerSchema),
    defaultValues: { name: '', email: stateEmail ?? '', password: '' },
    mode: 'onSubmit',
    reValidateMode: 'onBlur',
    shouldFocusError: true,
  })

  const mutation = useMutation({
    mutationFn: registerAccount,
    // O backend responde 202 igual para e-mail novo e para e-mail já cadastrado,
    // e envia e-mail nos dois casos (código, ou aviso de tentativa). Por isso a
    // tela segue sempre para a confirmação: não há caso "já existe" a tratar.
    onSuccess(result, variables) {
      // ADR-014: este token é a metade que fica COM QUEM PEDIU o código. Sem
      // guardá-lo, o código que chega no e-mail não confirma nada — e é por
      // isso que o código de um atacante, entregue na caixa da vítima, é
      // inútil na mão dela. Substitui qualquer token anterior: a tentativa
      // antiga deixou de ser a que esta tela está conduzindo. Vai amarrado ao
      // endereço deste pedido: é o par que o servidor consulta.
      saveRegistration(result.registrationToken, variables.email)
      clearResendCooldown()
      navigate({ to: '/confirmar-email', state: { email: variables.email } })
    },
  })

  const onSubmit = form.handleSubmit((values) => {
    if (mutation.isPending) return
    mutation.mutate(values)
  })

  return (
    <AuthLayout
      documentTitle="Criar conta"
      eyebrow="Nova conta"
      step="1 de 2"
      title="Criar conta"
      intro="Você vai receber um código de 6 dígitos por e-mail para confirmar o endereço."
      notice={flash ? <FlashAlert flash={flash} /> : undefined}
      footer={
        <p>
          Já tem conta? <TextLink to="/entrar">Entrar</TextLink>
        </p>
      }
    >
      <AuthForm
        onSubmit={onSubmit}
        error={mutation.error ? messageForError(mutation.error) : undefined}
        primary={
          <Button type="submit" variant="primary" fullWidth loading={mutation.isPending}>
            Criar conta
          </Button>
        }
      >
        <TextField
          {...form.register('name')}
          label="Nome"
          autoComplete="name"
          hint="Como você quer ser chamado no app."
          error={form.formState.errors.name?.message}
        />
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
        <PasswordField
          {...form.register('password')}
          label="Senha"
          autoComplete="new-password"
          showRequirement
          hint="Prefira uma frase que só você saiba."
          error={form.formState.errors.password?.message}
        />
      </AuthForm>
    </AuthLayout>
  )
}
