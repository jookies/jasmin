import { Create, useForm } from "@refinedev/antd";
import { InterceptorFields } from "./form";

export const InterceptorCreate = () => {
  const { formProps, saveButtonProps } = useForm();
  return (
    <Create saveButtonProps={saveButtonProps}>
      <InterceptorFields formProps={formProps} />
    </Create>
  );
};
